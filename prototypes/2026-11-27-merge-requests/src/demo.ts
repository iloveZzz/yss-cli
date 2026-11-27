export type Platform = 'GitLab' | 'GitHub'
export type Status = 'open' | 'merged' | 'closed'
export type Issue = 'unavailable' | 'unauthorized' | 'rateLimited'

export interface Repository {
  id: string
  platform: Platform
  slug: string
  host: string
}

export interface Source {
  id: string
  project: string
  hint: string
  repositories: { id: string; binding: string }[]
  chooseOne?: boolean
}

interface CheckEvidence {
  commit: string
  recordedAt: string
}

interface CheckSummary {
  status: string
  evidence?: CheckEvidence
  detail?: string
}

export interface MergeRequest {
  id: string
  repo: string
  number: number
  title: string
  author: string
  created: string
  updated: string
  ended?: string
  status: Status
  draft: boolean
  from: string
  fromRepository?: string
  to: string
  description: string
  descriptionUnavailable?: string
  approval: CheckSummary
  ci: CheckSummary
  historicalCi?: CheckEvidence & { status: string }
}

export const repositories: Repository[] = [
  { id: 'core', platform: 'GitLab', slug: 'nebula/platform-core', host: 'gitlab.example.test' },
  { id: 'web', platform: 'GitHub', slug: 'nebula/console-web', host: 'github.example.test' },
  { id: 'release', platform: 'GitLab', slug: 'nebula/release-tools', host: 'gitlab.example.test' },
  { id: 'shared', platform: 'GitHub', slug: 'nebula/ui-components', host: 'github.example.test' },
  { id: 'atlas', platform: 'GitLab', slug: 'nebula/job-runner', host: 'gitlab.example.test' },
  { id: 'archive', platform: 'GitHub', slug: 'nebula/job-runner-archive', host: 'github.example.test' },
  { id: 'core-mirror', platform: 'GitLab', slug: 'nebula/platform-core', host: 'gitlab-mirror.example.test' },
]
export const repoById = new Map(repositories.map(repo => [repo.id, repo]))
export const defaultEnabled = ['core', 'web', 'release']

export const sources: Source[] = [
  {
    id: 'core', project: '平台核心服务',
    hint: 'Home 绑定已核实。主目录和 2 个 worktree 指向同一仓库，列表只读取一次。',
    repositories: [{ id: 'core', binding: 'Home 绑定' }],
  },
  {
    id: 'web', project: '运营管理台',
    hint: '未配置有效 Home 绑定。已识别此工程的唯一 origin。',
    repositories: [{ id: 'web', binding: 'Git origin' }],
  },
  {
    id: 'release', project: '发布工具',
    hint: '已识别此工程的唯一 origin。',
    repositories: [{ id: 'release', binding: 'Git origin' }],
  },
  {
    id: 'shared', project: '前端组件库',
    hint: '未配置 Home 或 origin。可手动纳入此工程的 upstream。',
    repositories: [{ id: 'shared', binding: 'Git upstream · 手动纳入' }],
  },
  {
    id: 'worker', project: '异步任务调度',
    hint: 'Home 绑定失效。两个本机目录的 origin 指向不同仓库，可手动选定其中一个。',
    repositories: [
      { id: 'atlas', binding: '本机目录 A 的 origin' },
      { id: 'archive', binding: '本机目录 B 的 origin' },
    ],
    chooseOne: true,
  },
  {
    id: 'core-mirror', project: '平台核心服务镜像',
    hint: '已登记的镜像工程。可手动纳入此工程指向另一 GitLab 实例的 upstream。',
    repositories: [{ id: 'core-mirror', binding: 'Git upstream · 手动纳入' }],
  },
  {
    id: 'notes', project: '内部开发手册',
    hint: '本机目录没有可识别的 Git 远端。关联远端后可纳入。',
    repositories: [],
  },
]

type Seed = [
  repo: string, number: number, title: string, author: string, created: string, updated: string,
  status: Status, branch: string,
  options?: Partial<Pick<MergeRequest, 'draft' | 'description' | 'descriptionUnavailable' | 'approval' | 'ci' | 'historicalCi' | 'fromRepository' | 'to' | 'ended'>>,
]

const seeds: Seed[] = [
  ['core', 248, '支持按环境隔离发布配置', '许放', '2026-11-24 10:20', '2026-11-27 10:42', 'open', 'feat/environment-release-config', {
    description: '将预发与生产的发布参数拆分为独立配置，预发环境的调整不再覆盖生产环境。\n\n读取时会先核对目标环境，缺少对应配置时返回明确提示。',
    approval: { status: '等待审阅' }, ci: { status: '已通过' },
  }],
  ['web', 248, '优化管理台的表格批量操作反馈', '周祎', '2026-11-25 16:03', '2026-11-27 10:18', 'open', 'feat/batch-action-feedback', {
    draft: true, description: '统一批量处理的进度和失败提示，保留失败条目的选择，便于再次提交。', ci: { status: '运行中' },
  }],
  ['release', 86, '保留回滚前的部署摘要', '沈然', '2026-11-21 14:16', '2026-11-27 09:54', 'open', 'feat/rollback-summary', {
    approval: { status: '已批准' }, ci: { status: '未通过' },
  }],
  ['core', 241, '修复长任务重连时的状态回退', '林岑', '2026-11-14 09:45', '2026-11-26 18:07', 'open', 'fix/task-reconnect', {
    approval: { status: '已批准' }, ci: { status: '已通过' }, to: 'release/2.8',
  }],
  ['web', 252, '为用量明细增加日期分组', '姚宁', '2026-11-26 13:31', '2026-11-26 15:36', 'open', 'feat/usage-date-groups', { ci: { status: '已通过' } }],
  ['web', 239, '整理工作区的空列表提示', '魏慈', '2026-11-19 10:12', '2026-11-26 11:22', 'open', 'fix/workspace-empty-state', {
    approval: { status: '无需批准' }, ci: { status: '已通过' },
  }],
  ['release', 79, '补充单服务发布失败的重试说明', '许放', '2026-11-20 15:08', '2026-11-25 17:19', 'open', 'docs/release-retry', { draft: true }],
  ['core', 233, '延迟加载低频环境变量', '周祎', '2026-11-10 13:00', '2026-11-25 14:03', 'open', 'feat/lazy-environment-variables', { ci: { status: '运行中' } }],
  ['release', 88, '缩短构建报告保留周期', '姚宁', '2026-11-25 09:02', '2026-11-25 09:26', 'open', 'chore/report-retention'],
  ['core', 225, '统一 webhook 请求的时间格式', '沈然', '2026-11-07 16:48', '2026-11-24 16:55', 'open', 'fix/webhook-time-format', { ci: { status: '已通过' } }],
  ['web', 218, '支持复制运行记录的链接', '魏慈', '2026-10-29 08:40', '2026-11-21 11:18', 'open', 'feat/share-run-link', { ci: { status: '未通过' } }],
  ['core', 229, '修正工作线程的退出日志', '林岑', '2026-11-08 10:19', '2026-11-24 14:11', 'merged', 'fix/worker-exit-logs', { approval: { status: '已批准' }, ci: { status: '已通过' } }],
  ['release', 65, '将发布日志归档到构建产物', '许放', '2026-10-12 15:46', '2026-10-20 10:00', 'merged', 'feat/bundle-release-log', { approval: { status: '已批准' }, ci: { status: '已通过' } }],
  ['web', 31, '更新管理台依赖与构建配置', '姚宁', '2026-04-10 10:15', '2026-04-17 14:31', 'merged', 'build/refresh-dependencies', {
    fromRepository: 'yaoning/console-web',
    ended: '2026-04-16 17:20',
    description: '升级管理台的构建依赖，核对版本和静态资源输出路径。',
    approval: { status: '已批准', evidence: { commit: 'd318be6', recordedAt: '2026-04-16 15:46' } },
    ci: { status: '不可用', detail: '平台未返回当前提交的 CI 状态，可在原站查看。' },
    historicalCi: { status: '已通过', commit: '9a64c3e', recordedAt: '2026-04-15 14:51' },
  }],
  ['release', 74, '按团队重组部署脚本', '魏慈', '2026-11-03 16:44', '2026-11-18 10:10', 'closed', 'experiment/team-scripts', {
    ended: '2026-11-17 16:35',
    approval: { status: '不可用', detail: '当前身份无权读取审批信息，可在原站查看。' },
    ci: { status: '暂无记录' },
  }],
  ['core', 201, '试验日志存档的压缩策略', '林岑', '2026-09-25 10:14', '2026-10-06 11:02', 'closed', 'experiment/log-compression', {
    draft: true, descriptionUnavailable: '平台未返回描述内容，可在原站查看。',
  }],
  ['shared', 117, '为日期选择控件补充键盘操作', '周祎', '2026-11-23 09:50', '2026-11-26 08:37', 'open', 'feat/date-picker-keyboard', { ci: { status: '已通过' } }],
  ['atlas', 52, '支持按队列读取任务进度', '姚宁', '2026-11-20 14:43', '2026-11-23 15:22', 'open', 'feat/queue-progress'],
  ['archive', 19, '记录归档任务的退出原因', '沈然', '2026-11-18 08:29', '2026-11-19 11:48', 'closed', 'chore/archive-exit'],
  ['core-mirror', 248, '校验镜像实例的发布配置', '林岑', '2026-11-23 13:50', '2026-11-26 17:03', 'open', 'fix/mirror-release-validation', {
    description: '在镜像实例上核对发布目标和必填参数，补充校验失败时的说明。',
    approval: { status: '等待审阅' }, ci: { status: '运行中' },
  }],
]

export const requests: MergeRequest[] = seeds.map((seed) => {
  const [repo, number, title, author, created, updated, status, branch, options] = seed
  return {
    id: `${repo}-${number}`, repo, number, title, author, created, updated, status, from: branch,
    to: 'main', draft: false, description: '',
    approval: { status: '不可用', detail: '平台未返回审批摘要，可在原站查看。' },
    ci: { status: '不可用', detail: '平台未返回 CI 摘要，可在原站查看。' },
    ...options,
  }
})

export const tabs = [
  { id: 'open', label: '开放中' },
  { id: 'merged', label: '已合并' },
  { id: 'closed', label: '已关闭' },
  { id: 'all', label: '全部' },
] as const
export type Tab = typeof tabs[number]['id']

export const scenes = [
  { id: 'normal', label: '正常读取' },
  { id: 'partial', label: '部分仓库读取失败' },
  { id: 'empty', label: '未登记工程' },
  { id: 'auth', label: '授权失效' },
  { id: 'rate', label: '平台限流' },
] as const
export type Scene = typeof scenes[number]['id']

export const issues = {
  unavailable: {
    label: '连接失败',
    detail: '暂时无法连接。可重试读取受影响的仓库。',
  },
  unauthorized: {
    label: '授权失效',
    detail: '连接授权已失效。请在本机设置恢复授权后，返回刷新。',
  },
  rateLimited: {
    label: '平台限流',
    detail: '平台限制了读取频率。请稍后重试。',
  },
} satisfies Record<Issue, { label: string; detail: string }>

export function repositoryFor(id: string) {
  const repo = repoById.get(id)
  if (!repo) throw new Error(`Unknown demo repository: ${id}`)
  return repo
}

export function requestNumber(request: MergeRequest) {
  return `${repositoryFor(request.repo).platform === 'GitLab' ? '!' : '#'}${request.number}`
}

export function sourceUrl(request: MergeRequest) {
  const repo = repositoryFor(request.repo)
  const type = repo.platform === 'GitLab' ? '/-/merge_requests/' : '/pull/'
  return `https://${repo.host}/${repo.slug}${type}${request.number}`
}
