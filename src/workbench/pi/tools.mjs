import { createConnection } from "node:net";
import { Type } from "typebox";

const socketPath = process.env.YSS_PI_SOCKET;
const token = process.env.YSS_PI_SOCKET_TOKEN;
const allowCommands = process.env.YSS_PI_ALLOW_COMMANDS === "1";

function request(operation, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new Error("操作已取消"));
      return;
    }
    const socket = createConnection(socketPath);
    const chunks = [];
    let length = 0;
    const abort = () => socket.destroy(new Error("操作已取消"));
    signal?.addEventListener("abort", abort, { once: true });
    socket.once("close", () => signal?.removeEventListener("abort", abort));
    socket.once("connect", () => socket.write(JSON.stringify({ token, ...operation }) + "\n"));
    socket.once("error", reject);
    socket.on("data", (chunk) => {
      length += chunk.length;
      if (length > 8 * 1024 * 1024) {
        socket.destroy(new Error("工具响应过大"));
        return;
      }
      chunks.push(chunk);
    });
    socket.once("end", () => {
      try {
        const result = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        if (result.error) reject(new Error(result.error));
        else resolve(result);
      } catch {
        reject(new Error("工作台工具响应中断"));
      }
    });
  });
}

export default function workbench(pi) {
  const location = {
    path: Type.String({ description: "主工程相对路径，或已选 worktree 内的绝对路径" }),
    projectId: Type.Optional(Type.String({ description: "相对路径所属工程，缺省为主 Spec 工程" })),
  };
  const text = (description) => Type.String({ description });
  const tools = [];
  function register(name, label, description, properties) {
    tools.push(name);
    pi.registerTool({
      name, label, description, parameters: Type.Object(properties),
      async execute(id, args, signal) {
        const result = await request({ ...args, operation: name, id }, signal);
        return {
          content: [{ type: "text", text: result.text }],
          details: result.exitCode === undefined ? {} : { exitCode: result.exitCode },
          isError: result.exitCode != null && result.exitCode !== 0,
        };
      },
    });
  }
  register("read", "读文件", "读取隔离工程的 UTF-8 文件，可按行分页。", {
    ...location,
    offset: Type.Optional(Type.Integer({ minimum: 1, description: "起始行，默认 1" })),
    limit: Type.Optional(Type.Integer({ minimum: 1, maximum: 2000, description: "最多读取的行数" })),
  });
  register("ls", "列出文件", "列出工程内的目录，根目录使用空路径。", location);
  register("grep", "搜索文件", "按字面文本搜索工程内的 UTF-8 文件或目录，根目录使用空路径。", {
    ...location, query: text("要查找的原文"),
  });
  register("write", "写文件", "创建或覆盖隔离工程中的 UTF-8 文件。", {
    ...location, content: text("完整文件内容"),
  });
  register("edit", "编辑文件", "替换文件中恰好匹配一次的旧文本。", {
    ...location, oldText: text("完整旧文本"), newText: text("替换文本"),
  });
  register("ask_spec_question", "询问业务问题", "先在主 Spec 的 questions 登记业务问题，再用其 ID 请求用户答复。", {
    questionId: text("主 Spec 中已登记的业务问题 ID"),
  });
  if (allowCommands) {
    register("bash", "运行隔离命令", "在所选工程中运行离线 shell 命令。构建和测试使用已准备的依赖。", {
      ...location,
      path: Type.Optional(Type.String({ description: "工作目录，缺省为工程根" })),
      command: text("在 bash 中执行的命令"),
    });
  }
  pi.on("session_start", async () => {
    pi.setActiveTools(tools);
    await request({ operation: "ready", tools: pi.getActiveTools() });
  });
}
