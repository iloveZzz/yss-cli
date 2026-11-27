import { createConnection } from "node:net";
import { Type } from "typebox";

const socketPath = process.env.YSS_PI_SOCKET;
const token = process.env.YSS_PI_SOCKET_TOKEN;
const allowCommands = process.env.YSS_PI_ALLOW_COMMANDS === "1";
const hostCommands = process.env.YSS_PI_COMMAND_EXECUTION === "host";
const readOnly = process.env.YSS_PI_READ_ONLY === "1";
const nativeFirstRound = process.env.YSS_PI_NATIVE_FORK_READ_ONLY === "1";

function resultDetails(result) {
  const details = {};
  for (const key of ["exitCode", "terminationSignal", "commandDiagnostics", "commandExecution", "errorCode", "displayPath", "hint"]) {
    if (result[key] !== undefined) details[key] = result[key];
  }
  return details;
}

function request(operation, signal, attempt = 0) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new Error("操作已取消"));
      return;
    }
    const socket = createConnection(socketPath);
    const framed = process.platform === "win32";
    const chunks = [];
    let length = 0;
    let received = false;
    let connected = false;
    let retrying = false;
    const finish = () => {
      try {
        const result = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        if (result.error) {
          const message = [result.errorCode, result.displayPath, result.error, result.hint].filter(Boolean).join("\n");
          reject(Object.assign(new Error(message), { toolFailure: result }));
        } else resolve(result);
      } catch {
        reject(new Error("工作台工具响应中断"));
      }
    };
    const abort = () => socket.destroy(new Error("操作已取消"));
    signal?.addEventListener("abort", abort, { once: true });
    socket.once("close", () => {
      signal?.removeEventListener("abort", abort);
      if (framed && !received && !retrying) reject(new Error("工作台工具连接已关闭"));
    });
    socket.once("connect", () => {
      connected = true;
      socket.write(JSON.stringify({ token, ...operation }) + "\n");
    });
    socket.once("error", (error) => {
      if (framed && received) return;
      if (framed && !connected && error.code === "EBUSY" && attempt < 3 && !signal?.aborted) {
        retrying = true;
        setTimeout(() => resolve(request(operation, signal, attempt + 1)), 30);
      } else reject(error);
    });
    socket.on("data", (chunk) => {
      length += chunk.length;
      if (length > 8 * 1024 * 1024) {
        socket.destroy(new Error("工具响应过大"));
        return;
      }
      chunks.push(chunk);
      if (framed) {
        const newline = chunk.indexOf(10);
        if (received || (newline !== -1 && newline !== chunk.length - 1)) {
          socket.destroy(new Error("工作台工具响应格式无效"));
          return;
        }
        if (newline !== -1) {
          // A full response remains valid if the pipe closes during acknowledgment.
          // Write the acknowledgment without requesting a libuv pipe shutdown.
          received = true;
          socket.write("\n", () => socket.destroy());
          finish();
        }
      }
    });
    socket.once("end", () => {
      if (!framed) finish();
      else if (!received) {
        reject(new Error("工作台工具响应中断"));
      }
    });
  });
}

export default function workbench(pi) {
  const location = {
    path: Type.String({ description: "所选工程内的相对路径，或该工程内的绝对路径" }),
    projectId: Type.Optional(Type.String({ description: "相对路径所属工程，缺省为主 Spec 工程" })),
  };
  const text = (description) => Type.String({ description });
  const tools = [];
  function register(name, label, description, properties) {
    tools.push(name);
    pi.registerTool({
      name, label, description, parameters: Type.Object(properties),
      ...(process.platform === "win32" ? { executionMode: "sequential" } : {}),
      async execute(id, args, signal) {
        try {
          const result = await request({ ...args, operation: name, id }, signal);
          const text = typeof result.sourceHint === "string"
            ? `${result.text}\n\n${result.sourceHint}`
            : result.text;
          return {
            content: [{ type: "text", text }],
            details: resultDetails(result),
            isError: result.exitCode != null && result.exitCode !== 0,
          };
        } catch (error) {
          if (!error.toolFailure) throw error;
          return {
            content: [{ type: "text", text: error.message }],
            details: resultDetails(error.toolFailure),
            isError: true,
          };
        }
      },
    });
  }
  register("read", "读文件", "读取所选原工程的 UTF-8 文件，可按行分页。", {
    ...location,
    offset: Type.Optional(Type.Integer({ minimum: 1, description: "起始行，默认 1" })),
    limit: Type.Optional(Type.Integer({ minimum: 1, maximum: 2000, description: "最多读取的行数" })),
  });
  register("ls", "列出文件", "列出工程内的目录，根目录使用空路径。", location);
  register("grep", "搜索文件", "按字面文本搜索工程内的 UTF-8 文件或目录，根目录使用空路径。", {
    ...location, query: text("要查找的原文"),
  });
  if (!readOnly && !nativeFirstRound) {
    register("write", "写文件", "直接创建或覆盖原工程中的 UTF-8 文件。覆盖前先用 read 读取目标。", {
      ...location, content: text("完整文件内容"),
    });
    register("edit", "编辑文件", "直接替换原工程文件中恰好匹配一次的旧文本。编辑前先用 read 读取目标。", {
      ...location, oldText: text("完整旧文本"), newText: text("替换文本"),
    });
  }
  if (!nativeFirstRound) {
    register("ask_spec_question", "询问业务问题", "先在主 Spec 的 questions 登记业务问题，再用其 ID 请求用户答复。", {
      questionId: text("主 Spec 中已登记的业务问题 ID"),
    });
    register("update_plan", "更新计划", "在复杂任务中报告本轮实际步骤。每次提交完整列表，随执行更新状态。", {
      steps: Type.Array(Type.Object({
        title: Type.String({ minLength: 1, maxLength: 512 }),
        status: Type.Union([Type.Literal("pending"), Type.Literal("inProgress"), Type.Literal("completed")]),
      }), { minItems: 1, maxItems: 20 }),
    });
  }
  if (allowCommands && !nativeFirstRound) {
    const label = hostCommands ? "运行本机命令" : "运行离线命令";
    const description = hostCommands
      ? "在本轮授予 execute 权限的原工程目录使用本机 Bash。命令拥有当前用户的文件与网络权限。可以读取 Git 状态。保留分支、HEAD 和暂存区，变更会停止本轮。独立检查分别执行。"
      : "在所选工程中运行离线 shell 命令。Git 分支和 HEAD 使用工作台本轮已核验的信息，文件变化查看执行成果。必要的独立检查分别执行，构建和测试使用已准备的离线依赖，页面验证使用工作台预览。";
    register("bash", label, description, {
      ...location,
      path: Type.Optional(Type.String({ description: "工作目录，缺省为工程根" })),
      command: text(process.platform === "win32" ? "在 Windows cmd.exe 中执行的一行命令" : "在 bash 中执行的命令"),
    });
  }
  pi.on("session_start", async () => {
    pi.setActiveTools(tools);
    await request({ operation: "ready", tools: pi.getActiveTools() });
  });
  let lastUsage;
  let compaction = 0;
  async function reportUsage(_event, ctx) {
    try {
      const usage = ctx.getContextUsage();
      if (!usage || !Number.isSafeInteger(usage.contextWindow) || usage.contextWindow < 1) return;
      const usedTokens = Number.isSafeInteger(usage.tokens) ? usage.tokens : null;
      const key = `${usedTokens}/${usage.contextWindow}`;
      if (key === lastUsage) return;
      await request({ operation: "report_context", contextWindow: usage.contextWindow, usedTokens }, ctx.signal);
      lastUsage = key;
    } catch {
      // Statistics do not affect the tool or model run.
    }
  }
  pi.on("agent_start", reportUsage);
  pi.on("turn_end", reportUsage);
  function reportCompaction(status, ctx) {
    return request({ operation: "report_compaction", sequence: compaction, status }, ctx.signal).catch(() => {});
  }
  pi.on("session_before_compact", async (_event, ctx) => {
    compaction += 1;
    await reportCompaction("inProgress", ctx);
  });
  pi.on("session_compact", async (event, ctx) => {
    await reportCompaction("completed", ctx);
    await reportUsage(event, ctx);
  });
  pi.on("session_compact_failed", async (_event, ctx) => {
    await reportCompaction("failed", ctx);
  });
}
