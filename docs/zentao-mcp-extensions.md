# 禅道 MCP 扩展工具

除了从 OpenAPI 自动生成的接口级工具外，服务端还内置了一组 `zentao_*` **复合工具**。
它们把多次禅道 v1 调用合并成一次问答，解决“接口能调通但用起来仍然费劲”的问题：

- 不知道产品/迭代 ID 时无从下手；
- 查 Bug 必须先知道 productID，且没有关键字、日期、责任人过滤；
- AI 评分藏在对象的 `actions` 里，接口级工具看不到；
- 单个 Bug 的时间线、复现步骤、重新激活次数需要人工拼装；
- 想看一个迭代的质量分布或某人某月的工作量，只能自己翻页统计。

## 一：启用

在 `config.yaml` 的对应 server 下开启：

```yaml
servers:
  - name: zentao-test
    schema_url: "/docs/zentao-openapi.json"
    base_url: "http://10.70.33.19/biz/api.php/v1"
    skip_deprecated: true      # 隐藏禅道 12.3 v1 不支持的 12 个路由
    zentao_extensions: true    # 注册 zentao_* 复合工具
```

| 配置项 | 默认 | 说明 |
|---|---|---|
| `skip_deprecated` | `false` | 跳过 OpenAPI 中 `deprecated: true` 的操作。随附禅道文档用它标记 12.3 v1 会返回 404 的路由 |
| `zentao_extensions` | `false` | 注册本文档的 6 个复合工具。只有上游确实是禅道时才应开启 |

认证沿用现有机制：复合工具走同一个带鉴权的 HTTP 客户端，`zentao-account` / `zentao-password`
或 `token` 请求头都可以，服务端自动登录并缓存 token。

## 二：工具清单

| 工具 | 用途 | 关键参数 |
|---|---|---|
| `zentao_resolve_scope` | 关键字 → 产品/项目/执行/人员 ID | `keyword`、`kinds` |
| `zentao_search_bugs` | 跨范围查 Bug，多条件过滤 + 分布统计 | `productID`、`keyword`、`openedBy`、`month` |
| `zentao_analyze_bug` | 单个 Bug 的问题分析包 | `bugID` |
| `zentao_ai_score` | 对象与备注的 AI 评分 | `objectType`、`ids` 或 `scope`+`scopeID` |
| `zentao_quality_report` | Bug 质量统计与趋势 | `productID`/`executionID`、`month` |
| `zentao_user_worklog` | 某人某时间段的工作记录 | `account`、`month`、`executionIDs` |
| `zentao_find_similar_bugs` | 按现象检索历史问题（需开启 `bug_index`） | `symptom`、`productID`、`fixedOnly` |

所有工具都是**只读**的，不会创建或修改禅道数据。

### 1. zentao_resolve_scope —— 先找到 ID

其他工具需要 ID，这个工具负责把名字翻译成 ID。

```text
用 zentao-test 查一下名字里带“数据中心”的产品和迭代
```

返回每条匹配的 `kind`、`id`、`name`、`status`，以及 `usableAs`（这个 ID 能喂给哪个参数）。
`kinds` 可取 `product`、`project`、`execution`、`program`、`user`，默认前四类中的常用四种。
`keyword` 留空表示列出当前账号可读的条目。

### 2. zentao_search_bugs —— 查找 Bug

```text
用 zentao-test 查产品 654 里 2026 年 5 月由 chenpenglie 提的、标题带“导出”的 Bug
```

支持的过滤条件：

| 参数 | 说明 |
|---|---|
| `scope` + `scopeID` | `product` / `project` / `execution`；也可直接用 `productID`、`projectID`、`executionID` |
| `scopeIDs` | 同时查多个同类范围 |
| `keyword` | 在标题、重现步骤、关键词里模糊匹配 |
| `status` | `active`、`resolved`、`closed`、`all` |
| `resolution` | `fixed`、`postponed`、`bydesign`、`duplicate`、`notrepro`、`willnotfix` |
| `severity` / `pri` / `type` / `module` | 严重程度、优先级、类型、模块 |
| `assignedTo` / `openedBy` / `resolvedBy` | 支持账号，也支持禅道返回了 `realname` 的姓名 |
| `month` 或 `openedAfter`/`openedBefore` | 创建时间窗口 |
| `resolvedAfter` / `resolvedBefore` | 解决时间窗口 |
| `limit` / `maxScan` | 返回条数（默认 20）与最多扫描条数（默认 600） |

返回结构：

- `bugs`：匹配到的 Bug，含 `fixDays`（创建到解决的天数）和 `stepsPreview`；
- `facets`：全部匹配项按状态、严重程度、类型、解决方案、责任人的分布（不受 `limit` 影响）；
- `scopes`：每个范围扫了多少条、上游总数、是否被截断，单个范围失败不会影响其他范围；
- `notes`：预算耗尽、结果被截断等需要用户知道的信息。

不传任何 ID 时会退化成“扫描当前账号可读的前若干个产品”，速度慢且结果可能不全，
`notes` 里会明确提示——正式使用时建议先用 `zentao_resolve_scope` 拿到 ID。

### 3. zentao_analyze_bug —— 问题分析

```text
用 zentao-test 分析 Bug 88123 的原因
```

一次调用返回：

- `bug`：基本信息；
- `content.steps`：HTML 转成纯文本的重现步骤；`content.resolutionNote`：解决时填写的说明；
- `timeline`：完整动作时间线（创建、指派、备注、激活、解决、关闭），含操作人和时间；
- `comments`：逐条备注（含各自的 AI 评分）；
- `metrics`：创建→解决、解决→关闭、创建→关闭天数，动作数、备注数、**重新激活次数**、已评分备注数；
- `related`：同产品下标题相似或同模块的 Bug（Jaccard 相似度 + 同模块判定），用于回归与同类问题排查；
- `analysisChecklist`：根因分析检查清单（复现、定位、根因分类、影响范围、修复方案、验证、预防），
  提示模型按证据分点作答，缺证据的地方标“待确认”。

工具本身不臆测根因，它只负责把分析所需的证据整理齐全。

### 4. zentao_ai_score —— 询问 AI 评分

禅道企业版有两套分值，实测（10.70.33.19，2026-09）如下：

| 位置 | 字段 | 量纲 | 示例 |
|---|---|---|---|
| 对象本身 | `aiScore` | 0-100 | 任务 94847 = `"97"`，Bug 69682 = `"65"` |
| 每条独立备注 | `actions[].score` | 0-5 | `4`、`4.5`、`5` |

注意备注的分值字段名是 `score` 而不是 `aiScore`，且两套分值**量纲不同，不能混在一起求平均**。
工具会优先读 `aiScore`、回退到 `score`，并在 `notes` 里提示量纲差异。

```text
用 zentao-test 看看任务 84735 的 AI 评分
用 zentao-test 统计执行 4058 最近 20 个任务的 AI 评分情况
```

参数：`objectType`（`task`/`bug`/`story`/`testcase`，默认 `task`）、`ids`，
或 `scope` + `scopeID`（也可用 `executionID`、`productID`、`projectID`）批量取最近的对象。

返回每个对象的 `objectAiScore`、备注总数、已评分数、未评分数、平均/最低/最高分、
`scoringComplete`，以及汇总的 `scoringCoveragePercent`。
未评分是正常现象——禅道异步打分，`notes` 会提示稍后重查。

### 5. zentao_quality_report —— 质量统计

```text
用 zentao-test 统计产品 654 在 2026 年上半年的 Bug 质量情况
```

返回状态/严重程度/类型/解决方案分布、模块与责任人 TOP 榜、
修复时长（平均/中位数/P90/最长）、重新激活率、未确认数、逐月新增与解决趋势、
最久未处理的激活 Bug，以及中文 `highlights` 结论。

扫描上限由 `maxScan` 控制（默认 1000）；被截断时 `notes` 会说明统计不完整。

### 6. zentao_user_worklog —— 个人工作量核对

```text
用 zentao-test 查 chenpenglie 在 2026 年 7 月、执行 4058 和 4102 下的工作记录
```

按账号和时间窗口列出任务、Bug、需求，并标注每条记录的 `roles`：
`opened`（他创建）、`finished`（他完成的任务）、`resolved`（他解决的 Bug）、
`closed`（他关闭的需求）、`assigned`（指派给他）。

必须给出 `executionIDs`、`productIDs` 或 `projectIDs` 之一——禅道 v1 没有全局的按人查询接口，
不给范围就无法保证结果完整，所以工具选择直接报错而不是返回一份不可信的清单。

与 `zentao_tool`（月度工作汇总 skill）配合时，这个工具适合在上传后做**独立复核**：
`zentao_user_worklog` 确认记录数和归属，`zentao_ai_score` 确认备注数量与评分。

### 7. zentao_find_similar_bugs —— 按现象查历史问题

现网设备暴露出一个现象，想知道历史上有没有同类问题、当时怎么解决的。

```text
用 zentao-test 查一下历史上有没有类似问题：DCMG100 网关重启后上报 Inform 报文，
DeviceInfo.SpecVersion 节点参数异常
```

**为什么需要单独的索引。** 禅道 v1 **完全没有文本检索接口**——实测
`search`/`query`/`keyword`/`title`/`q`/`param`/`searchValue` 七个参数名全部被静默忽略，
返回结果和不传时完全一致；`browseType` 同样是空操作。跨产品实时扫描约 50 秒，不可用。
所以服务端自建一份内存倒排索引。

**实测数据（10.70.33.19，2026-09）：**

| | |
|---|---|
| 语料 | 17,684 个 Bug / 89 个产品 / 2022-03 起 |
| 建索引 | 拉取约 50s + 构建约 2.4s |
| 查询 | 约 30ms |
| 其中已修复 | 10,453（59%） |

**中文分词用二元字组，不用 trigram。** SQLite FTS5 的 trigram 分词器对两字词返回空结果
（`重启`、`告警`、`速率` 实测 0 命中），而缺陷描述里大量是两字词。本索引把中文切成
重叠二元字组，英数字串整体保留（`DCMG150`、`FXM6000` 这类型号不会被切碎）。

**排序** = BM25 × 已修复 1.35 × codeerror 1.15 × 型号/省份精确命中 1.6。
标题词权重是正文的两倍。标题里的 `【】` 会被抽成 facet——本语料 81% 的 Bug 带这种标签，
里面正是设备型号、省份和运营商。

**权限（重要）。** 索引由一个服务账号构建，因此装着该账号能看到的全部内容。
查询结果**绝不直接取自索引**：排序后每条候选都会用**调用者自己的凭据**重新读一次，
读不到的直接丢弃，只回报 `hiddenByPermission` 的数量、不回报其 ID 或内容。
返回的每个字段都来自这次授权读取。

返回内容包括标题、类型、严重程度、解决方案、解决人、重现步骤摘要、
**当时的解决说明**（`fixNote`，取自 resolved 动作的备注）、备注数和重新激活次数。
拿到候选后可再对最像的 1-3 条调 `zentao_analyze_bug` 看完整时间线。

工具只做词面相似，不做语义判断——是否真的同一个问题，仍需人工或模型确认。

## 三：与接口级工具的关系

| 场景 | 用哪个 |
|---|---|
| 查询、统计、分析 | `zentao_*` 复合工具 |
| 创建/修改任务、Bug、需求 | 自动生成的接口级工具（`post_executions_executionID_tasks` 等） |
| 复合工具没覆盖的字段 | 接口级工具直接调对应接口 |

复合工具只读，写操作仍然走原来的接口级工具，权限和 allow/block 规则不变。

## 四：限制

1. **禅道 v1 没有备注写入接口。** AI 评分依赖的独立备注只能通过禅道 Web 的
   `action-comment-{type}-{id}.html` 提交，本服务不做这件事；写备注仍需 `zentao_tool`。
2. **没有全局搜索接口。** 所有查询都是“按范围拉取 + 服务端过滤”，因此需要 ID，
   并受 `maxScan` 限制。范围越明确越快越准。
3. **相似 Bug 用的是标题相似度**（英文按词、中文按二元字组），不是语义检索，
   只用于提示“可能相关”，需要人工确认。
4. **依赖 `actions` 字段。** 已实测禅道 12.3 v1 的 Bug/任务详情确实返回 `actions`
   （Bug 69627 有 7 条，任务 88916 有 15 条）。若上游未返回，
   时间线、备注数和评分会为空，工具会在 `notes` 里说明。

## 五：历史问题检索（zentao_find_similar_bugs）

按现象描述找历史上处理过的同类 Bug。需要 `bug_index.enabled: true`，否则该工具
**不会出现在工具列表里**（而不是出现后报错）。

### 索引怎么建

| | 实测值（10.70.33.19，服务账号 admin） |
|---|---|
| 语料 | 67,016 条 Bug / 623 个产品 |
| 构建耗时 | 约 2 分钟（8 路并发），启动时异步进行，不阻塞服务 |
| 内存 | 约 380 MB，常驻进程内存 |
| 刷新 | 默认每 6 小时全量重建 |
| 落盘 | **不落盘**。容器重启即重建 |

`max_products` 和 `max_bugs_per_product` 必须覆盖服务账号的实际可见范围，
否则多出来的部分静默不进索引。先查 `GET /products?limit=1` 的 `total` 再定值。

### 权限模型（重要）

索引装的是**服务账号能看到的全部内容**，但**查询结果永远不直接取自索引**：

1. 索引只负责排序，返回的候选只有 id、分数和命中词；
2. 每条候选都用**调用者自己的凭据**重新读一次；
3. 读不到的直接丢弃，只回报数量，不返回任何内容。

因此服务账号可见范围越广，召回越好，而调用者仍然只能看到自己有权看的记录。

**禅道用 HTTP 400 表示"读不到"**：12.3 v1 对无权访问的 Bug 返回
`400 {"error":"error"}`，而不是 401/403/404（实测：同一条 Bug，admin 读成功、
可见范围外的账号读返回 400）。所以 400 被归类为"无权限或已删除"，而不是临时故障。

由于服务账号通常比调用者可见得多，多数候选会被过滤掉，因此内部按 8 倍超取候选，
分批回查、够数即停。

## 六：时区（重要）

禅道 v1 在同一份报文里混用两种时间格式，实测同一事件：

```text
对象字段  openedDate = "2026-08-31T08:18:28Z"   ← UTC
动作字段  actions[].date = "2026-08-31 16:18:28"  ← 本地时间（+08）
```

工具按 `timezone`（默认 `Asia/Shanghai`）把带时区的值换算成禅道本地日期，
不带时区的值直接当作本地时间。**不做这一步的话，北京时间 0:00-8:00 创建的记录
会被算到前一天**，`month=2026-09` 之类的过滤会漏掉月初、混入下月初的数据。
