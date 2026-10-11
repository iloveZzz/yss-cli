/** Execute real commands and retain evidence even when a test crashes or is cancelled. */
import fs from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

export async function runCommands(commands, {
  cwd = process.cwd(), env = process.env, outDir, failFast = false,
  heartbeatMs = 30_000, timeoutMs = 45 * 60_000, output = text => process.stdout.write(text),
} = {}) {
  if (!outDir) throw new Error("验证报告必须指定 outDir");
  fs.mkdirSync(outDir, { recursive: true });
  const eventsFile = path.join(outDir, "test-events.jsonl");
  const logFile = path.join(outDir, "commands.log");
  fs.writeFileSync(eventsFile, "");
  fs.writeFileSync(logFile, "");
  const tests = new Map(), active = new Set();
  const report = { schemaVersion: 1, startedAt: new Date().toISOString(), exitCode: 0,
    commands: commands.map(c => ({ id: c.id, command: c.command, args: c.args, status: "unexecuted" })), tests: [] };
  const log = text => { fs.appendFileSync(logFile, text); output(text); };
  const save = () => {
    report.tests = [...tests.values()].sort((a, b) => (b.elapsed ?? 0) - (a.elapsed ?? 0));
    fs.writeFileSync(path.join(outDir, "test-timings.json"), JSON.stringify(report, null, 2) + "\n");
  };
  let cancelled = false, stop;
  const cancel = () => { cancelled = true; stop?.("cancelled"); };
  process.on("SIGINT", cancel);
  process.on("SIGTERM", cancel);
  try {
    for (const [index, command] of commands.entries()) {
      if (cancelled || (failFast && report.exitCode)) break;
      const row = report.commands[index], started = performance.now();
      Object.assign(row, { status: "running", startedAt: new Date().toISOString() });
      active.clear();
      log(`[verification] start ${command.id}: ${command.command} ${command.args.join(" ")}\n`);
      save();
      await new Promise(resolve => {
        const child = spawn(command.command, command.args, {
          cwd: command.cwd ?? cwd, env, stdio: ["ignore", "pipe", "pipe"], detached: process.platform !== "win32",
        });
        let stopped, killTimer;
        const kill = signal => {
          if (!child.pid) return;
          try {
            if (process.platform === "win32") child.kill(signal);
            else process.kill(-child.pid, signal);
          } catch (error) { if (error.code !== "ESRCH") row.cleanupError = error.message; }
        };
        stop = reason => {
          if (stopped) return;
          stopped = reason;
          kill("SIGTERM");
          killTimer = setTimeout(() => kill("SIGKILL"), 1000);
        };
        const timer = setTimeout(() => stop("timed_out"), command.timeoutMs ?? timeoutMs);
        const heartbeat = setInterval(() => {
          log(`[verification] ${command.id} elapsed=${Math.round((performance.now() - started) / 1000)}s active=${[...active].join(", ") || "command/compile"}\n`);
          save();
        }, heartbeatMs);
        createInterface({ input: child.stderr }).on("line", line => log(line + "\n"));
        createInterface({ input: child.stdout }).on("line", line => {
          let event;
          if (command.goJSON) { try { event = JSON.parse(line); } catch {} }
          if (!event?.Action || !event.Package) { log(line + "\n"); return; }
          fs.appendFileSync(eventsFile, JSON.stringify({ command: command.id, ...event }) + "\n");
          const name = `${event.Package}/${event.Test ?? "(package)"}`;
          const key = `${command.id}:${name}`;
          if (["run", "cont", "start"].includes(event.Action)) active.add(name);
          if (["pause", "pass", "fail", "skip"].includes(event.Action)) active.delete(name);
          if (event.Action !== "output") {
            tests.set(key, { command: command.id, package: event.Package, test: event.Test ?? null,
              status: event.Action, elapsed: event.Elapsed, time: event.Time });
            log(`[test] ${event.Action} ${name}${event.Elapsed === undefined ? "" : ` ${event.Elapsed}s`}\n`);
          }
          if (event.Output) log(event.Output);
        });
        child.on("error", error => { row.error = error.message; });
        // A failed Go test binary may leave CLI/Node descendants holding stdout open.
        child.on("exit", () => kill("SIGKILL"));
        child.on("close", (code, signal) => {
          clearTimeout(timer); clearTimeout(killTimer); clearInterval(heartbeat); stop = undefined;
          Object.assign(row, { exitCode: code ?? (cancelled ? 130 : 1), signal,
            status: stopped ?? (code === 0 ? "passed" : "failed"), elapsed: (performance.now() - started) / 1000 });
          row.missingTests = (command.expectedTests ?? []).filter(item => {
            const observed = tests.get(`${command.id}:${item.package}/${item.test}`);
            return !observed || !["pass", "fail", "skip"].includes(observed.status);
          }).map(item => `${item.package}/${item.test}`);
          if (row.missingTests.length && row.status === "passed") row.status = "incomplete";
          if (row.status !== "passed") report.exitCode ||= row.exitCode || 1;
          for (const item of tests.values()) if (item.command === command.id && !["pass", "fail", "skip"].includes(item.status)) item.status = "interrupted";
          log(`[verification] ${row.status} ${command.id} ${row.elapsed.toFixed(3)}s\n`);
          save();
          resolve();
        });
      });
    }
  } finally {
    process.off("SIGINT", cancel); process.off("SIGTERM", cancel);
    if (cancelled) report.exitCode ||= 130;
    report.completedAt = new Date().toISOString();
    save();
  }
  return report;
}
