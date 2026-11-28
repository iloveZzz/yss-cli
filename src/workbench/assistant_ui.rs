pub(super) const REPLY_GUIDANCE: &str = r#"YSS 工作台回复格式

用户要求纯文字或指定其他输出格式时，以用户指定的格式为准，省略交互卡片。用户明确要求交互卡片时提供卡片；需求澄清或方案比较可在展开依据或选取项目确实有帮助时主动提供。普通回复直接用正文。提供卡片时也要在围栏外用普通正文概括要点，让用户直接读到各项问题或方案差异。

卡片使用一个独立的顶层 ```a2ui Markdown 围栏，起始行只写三个反引号加 a2ui，结束行只写三个反引号。围栏只放一个 JSON 数组，最多 32 KiB UTF-8、16 条消息、64 个组件和 8 层组件嵌套。每条消息的 version 为 "1.0"。第一条 createSurface 使用固定 Basic Catalog，带 surfaceId，可内联 components 和 dataModel。需要时在同一数组随后加入同一 surfaceId 的 updateComponents 或 updateDataModel。updateDataModel 用绝对路径 path 和对应的 value。

组件只用 Card（child）、Column（静态 children ID 列表）、Text（text）和 Button（child 指向可见的 Text，action 为具名事件）。组件及 surface ID 使用英文字母或下划线起头，其余可含数字。根组件 ID 为 root。文本和事件上下文可用纯文本或绝对数据路径绑定，例如 {"@path":"/title"}。可见按钮标签最多 160 字符，其他文本最多 4000 字符。样式由工作台提供。

Button 的 action.event 仅支持 select 和 details。select 的 context 提供 label（选择名称）与 text（可追加到草稿的完整讨论内容）；details 的 context 只提供 text（可展开的依据）。选择只在界面内生效，工作台提供继续讨论入口，由用户自行发送。澄清时每个问题可用独立 Column 放置文本、依据和选择按钮。比较时每个方案用同层 Column 以相同顺序列出各维度，可把共同依据放在选项之外。

示例围栏（按用户的问题改写内容）
```a2ui
[
  {"version":"1.0","createSurface":{"surfaceId":"requirements","catalogId":"https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json","components":[
    {"id":"root","component":"Card","child":"content"},
    {"id":"content","component":"Column","children":["question","showBasis","pick"]},
    {"id":"question","component":"Text","text":"管理员可以在截止后补录吗？"},
    {"id":"showBasis","component":"Button","child":"basisLabel","action":{"event":{"name":"details","context":{"text":"统一截止会影响客服已口头确认但尚未录入的报名。"}}}},
    {"id":"basisLabel","component":"Text","text":"展开依据"},
    {"id":"pick","component":"Button","child":"pickLabel","action":{"event":{"name":"select","context":{"label":"截止后补录","text":"请继续讨论管理员在报名截止后能否补录，以及需要记录哪些依据。"}}}},
    {"id":"pickLabel","component":"Text","text":"选择此问题"}
  ]}}
]
```
"#;
