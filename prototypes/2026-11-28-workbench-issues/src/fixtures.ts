export interface IssueSource {
  id: string
  provider: 'GitHub' | 'GitLab'
  project: string
  repository: string
  sourceBasis: string
  codeRepository: string
}

export interface Issue {
  sourceId: string
  number: number
  title: string
  state: 'open' | 'closed'
  labels: string[]
  updatedAt: string
  body: string
  url: string
}

export const sources: IssueSource[] = [
  {
    id: 'desktop-editor', provider: 'GitHub', project: '桌面编辑器',
    repository: 'microsoft/vscode', sourceBasis: 'Issue 跟踪配置', codeRepository: 'microsoft/vscode-docs',
  },
  {
    id: 'collaboration', provider: 'GitLab', project: '协作平台',
    repository: 'gitlab-org/gitlab', sourceBasis: '工程远端', codeRepository: 'gitlab-org/gitlab',
  },
]

const editorBody = `从启动器打开已有工作区时，编辑区会先出现一帧浅色背景，再恢复上次使用的深色主题。连续打开两个窗口时，第二个窗口更容易出现。

## 复现步骤

1. 将工作区切换到深色主题，保留两个编辑器标签页。
2. 完全退出应用，再从启动器打开同一工作区。
3. 在文件树加载完成前观察编辑区的背景。

> 录屏中的“潮汐窗口”是观察员标记的闪屏时段。侧栏和状态栏在这段时间内颜色稳定。

## 观察记录

| 场景 | 观察结果 |
| --- | --- |
| 首次打开空窗口 | 使用默认背景，未观察到闪屏 |
| 恢复深色工作区 | 首帧出现浅色背景 |
| 在已打开的窗口内切换文件 | 界面颜色保持一致 |

### 记录的设置

\`\`\`json
{
  "window.restoreWindows": "all",
  "workbench.colorTheme": "Default Dark Modern"
}
\`\`\`

## 核对清单

- [x] 保存启动录屏
- [ ] 在高对比度主题复核
- [ ] 对比首次启动与会话恢复

用同一个工作区复核 [观察笔记](./notes/startup.md)，并记录不同窗口尺寸下的表现。

![工作区恢复时的首帧](./attachments/first-frame.png)
`

const collaborationBody = `部分目录服务会在首次同步时返回空密钥字段。管理员需要区分“没有配置同步”和“目录暂未返回密钥”。

## 配置检查

- 在测试组开启密钥同步，保存一次正常同步结果。
- 将一位成员的密钥字段留空，查看同步记录。
- 检查原有密钥的归属与更新时间。

> 排查记录中的“雪线标记”对应首次出现空字段的同步批次。

| 输入 | 期望反馈 |
| --- | --- |
| 可读取的密钥字段 | 显示本次读取结果 |
| 空字段 | 给出可定位的配置提示 |

- [x] 记录正常同步
- [ ] 复核空字段的提示

详情见[同步记录附件](/uploads/example/sync-log.txt)。`

type Fixture = [number: number, title: string, state: Issue['state'], labels: string[], updatedAt: string, body: string]

// Real repository/issue identities; the Chinese summaries, content and metadata are demonstration data.
const editor: Fixture[] = [
  [8, '打开工作区时编辑器短暂闪屏', 'open', ['缺陷', '工作台'], '2026-11-28T09:46:00+08:00', editorBody],
  [5, '欢迎页打包路径有误', 'open', ['打包'], '2026-11-28T08:15:00+08:00', ''],
  [4, 'C# 括号补全后缩进偏移', 'open', ['缺陷', '编辑器'], '2026-11-27T16:10:00+08:00', '在已有代码块中输入左花括号，换行后光标落在上一层缩进。多次嵌套后更明显。'],
  [10, '测试启动时缺少 sinon 模块', 'open', ['测试'], '2026-11-26T10:20:00+08:00', '全新检出后第一次运行测试时，依赖解析停在 `sinon`。已有缓存的工作区未出现这一提示。'],
  [7, '从开发目录启动编辑器失败', 'closed', ['开发环境'], '2026-11-25T14:32:00+08:00', '开发目录的启动说明已更新，按新的安装顺序可打开主窗口。'],
  [6, 'Windows 启动后语言服务没有就绪', 'open', ['Windows'], '2026-11-24T17:05:00+08:00', '打开解决方案后，状态栏一直显示正在启动。日志中只有首次连接的时间。'],
  [1, '整理编辑器的开源发布材料', 'closed', ['文档'], '2026-11-23T09:30:00+08:00', '发布清单已包含许可证、贡献指南与构建入口。'],
  [3, 'Linux 安装包缺少语言服务', 'open', ['Linux', '打包'], '2026-11-22T13:14:00+08:00', '在干净的环境安装后，编辑器可以启动，打开 C# 文件时没有补全提示。'],
  [9, '扩展主机日志没有区分空值类型', 'open', [], '2026-11-21T15:38:00+08:00', '扩展输出 `null` 和 `undefined` 时显示相同，需要保留原值以便定位。'],
  [2, '打包后符号链接丢失原始属性', 'open', ['打包'], '2026-11-20T11:00:00+08:00', '比对构建目录和分发目录，部分符号链接的属性发生变化。'],
]

const collaboration: Fixture[] = [
  [8, '校验 LDAP 的 SSH 密钥同步设置', 'open', ['配置', '目录同步'], '2026-11-28T09:32:00+08:00', collaborationBody],
  [7, '列表中的 Issue 标题没有对齐', 'open', ['缺陷', '工作台'], '2026-11-27T19:30:00+08:00', '长标题换行后，第二行起点与首行图标对齐，影响快速浏览。'],
  [5, '允许单独配置 Jira 集成地址', 'open', ['集成'], '2026-11-27T11:25:00+08:00', '同一个项目组可能使用不同的 Jira 项目，维护者希望分别保存地址。'],
  [10, '广播消息中显示格式化文本', 'open', ['文档'], '2026-11-26T09:05:00+08:00', '管理员在广播编辑器中输入了以下内容。请核对阅读模式中的显示。\n\n<strong>维护公告</strong>\n\n可参考 [GitLab Markdown 文档](https://docs.gitlab.com/user/markdown/)。'],
  [3, '进入群组设置后侧栏选中项偏移', 'closed', ['导航'], '2026-11-25T12:40:00+08:00', '从群组主页进入设置，侧栏现在保留正确的当前项。'],
  [1, '打开审批人设置时提示服务错误', 'open', ['缺陷'], '2026-11-24T18:30:00+08:00', '管理员进入审批人设置后看到错误页；同一项目的其他设置可读。'],
  [6, '提交说明中的 Jira 编号显示为 #0', 'open', ['集成'], '2026-11-23T15:20:00+08:00', '外部编号被显示为本仓库编号，请核对描述中的引用。'],
  [4, '调整安装说明中的迁移顺序', 'closed', ['文档'], '2026-11-22T08:35:00+08:00', '安装文档已按实际执行顺序重排。'],
  [9, '合并前重放没有更新源分支', 'open', ['合并请求'], '2026-11-21T13:30:00+08:00', '源分支落后后选择重放，页面显示已完成，提交列表仍保留旧基线。'],
  [2, '分支名一致时重新核对合并结果', 'open', [], '2026-11-20T10:10:00+08:00', ''],
]

function makeIssues(source: IssueSource, rows: Fixture[]): Issue[] {
  const base = source.provider === 'GitHub'
    ? `https://github.com/${source.repository}/issues/`
    : `https://gitlab.com/${source.repository}/-/work_items/`
  return rows.map(([number, title, state, labels, updatedAt, body]) => ({
    sourceId: source.id, number, title, state, labels, updatedAt, body, url: base + number,
  }))
}

export const issues: Issue[] = [...makeIssues(sources[0]!, editor), ...makeIssues(sources[1]!, collaboration)]
