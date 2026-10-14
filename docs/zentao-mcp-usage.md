# 禅道 MCP 使用说明

本文说明如何安装和使用禅道 MCP。日常优先连接公网正式服务；内网正式服务保留为兜底，测试库仅在公司内网可用。

## 1. 环境说明

服务地址与上游环境：

| MCP 名称 | MCP 地址 | 上游禅道环境 | 说明 |
|---|---|---|---|
| `zentao-cloud` | `https://neoflow-cn.neo-net.com/zentao-mcp/mcp` | 正式禅道的公网 API 入口 | **默认使用**，无需公司内网；需要 `X-Mcp-Key` 和个人禅道凭据 |
| `zentao` | `http://10.70.2.73:7892/zentao/mcp` | `http://10.70.33.19/biz/api.php/v1` | **正式库**，日常默认用这个。已禁用删除接口 |
| `zentao-sandbox` | `http://10.70.2.73:7892/zentao-sandbox/mcp` | `http://10.70.33.18/biz/api.php/v1` | 测试库，写操作演练用。账号密码与正式库不同 |

> **旧名字已废弃**：`zentao-test` 曾经指向的是**正式库**、`zentao-prod` 指向测试库，两者是反的。
> 这两个名字已被删除而不是重新指向——继续用旧地址会直接报 404，请改成上表的新地址。

客户端显示名可以自定，服务 URL 必须与上表一致。Streamable HTTP 使用 `/mcp`，SSE 使用 `/sse`，两者不能混配。日常使用时直接用自然语言说明环境和操作即可。

### 1.1 千问办公等图形客户端

选择 `Streamable HTTP`，URL 填公网地址，添加三个 Headers：

| Header 名 | 值 | 用途 |
|---|---|---|
| `X-Mcp-Key` | 维护者提供的访问密钥 | 通过公网 nginx 的入口保护 |
| `zentao-account` | 用户自己的正式禅道账号 | 决定可读、可写范围 |
| `zentao-password` | 该用户的正式禅道密码 | 服务端自动换取并刷新 token |

也可用 `token` 替代最后两个 Header，公网仍需要 `X-Mcp-Key`。索引用的 `mcpread` 是服务器专用账号，不需要发给全员共用；扩大索引账号权限不会扩大调用者权限。

Headers 的普通键/值栏填写实际值；“来自环境变量的标头”栏右侧填写变量名。推荐工具超时 120 秒，保存后重新连接服务。

## 2. Codex 配置

推荐使用账号密码模式，由 MCP 服务端自动登录禅道、缓存 token、在 token 失效后自动刷新。

```toml
[mcp_servers.zentao]
enabled = true
url = "https://neoflow-cn.neo-net.com/zentao-mcp/mcp"
tool_timeout_sec = 120
env_http_headers = { "X-Mcp-Key" = "ZENTAO_MCP_KEY", "zentao-account" = "ZENTAO_ACCOUNT", "zentao-password" = "ZENTAO_PASSWORD" }

[mcp_servers.zentao-intranet]
enabled = false
url = "http://10.70.2.73:7892/zentao/mcp"
env_http_headers = { "zentao-account" = "ZENTAO_ACCOUNT", "zentao-password" = "ZENTAO_PASSWORD" }

[mcp_servers.zentao-sandbox]
enabled = false
url = "http://10.70.2.73:7892/zentao-sandbox/mcp"
env_http_headers = { "zentao-account" = "ZENTAO_SANDBOX_ACCOUNT", "zentao-password" = "ZENTAO_SANDBOX_PASSWORD" }
```

配置保存在用户的 `~/.codex/config.toml`。默认公网服务需要设置三个环境变量：

```powershell
[Environment]::SetEnvironmentVariable("ZENTAO_ACCOUNT", "你的禅道账号", "User")
[Environment]::SetEnvironmentVariable("ZENTAO_PASSWORD", "你的禅道密码", "User")
[Environment]::SetEnvironmentVariable("ZENTAO_MCP_KEY", "维护者提供的访问密钥", "User")
```

上面是占位示例，不要把真实值提交到项目或放进共享截图。环境变量没有目录要求，User 作用域持久保存；设置后完全退出并重启 Codex。测试库使用单独的两项 `ZENTAO_SANDBOX_*`，正式和测试密码不能混用。

配置字段已按 [OpenAI 官方配置参考](https://learn.chatgpt.com/docs/config-file/config-reference)核对：`http_headers` 放字面值，`env_http_headers` 放环境变量名。

### 2.1 给安装 Agent 的提示词

```text
请在我的客户端配置禅道 MCP：Streamable HTTP，地址为
https://neoflow-cn.neo-net.com/zentao-mcp/mcp。
配置 X-Mcp-Key、zentao-account、zentao-password 三个请求头；凭据由我在本地填写，
不要输出密码、密钥或 token，不要写入项目仓库。Codex 使用 env_http_headers 时，
右侧填写变量名。配置后提示重新连接，通过客户端实际连接获取工具列表，
确认 zentao_find_similar_bugs 存在，并执行一次只读历史 Bug 查询。
内网 curl 只能从能访问内网的电脑执行；云端沙箱内网不通不能证明本机客户端失败。
正式库是 zentao，sandbox 是另一套密码，默认不要启用 sandbox。
```

## 3. 兼容旧 Token 模式

旧的 token header 方式仍然保留，可用于临时兼容历史客户端。

```toml
[mcp_servers.zentao]
enabled = true
url = "https://neoflow-cn.neo-net.com/zentao-mcp/mcp"
env_http_headers = { "X-Mcp-Key" = "ZENTAO_MCP_KEY", token = "ZENTAO_TOKEN" }
```

认证优先级：

1. 同时存在 `zentao-account` / `zentao-password` 和 `token` 时，优先使用账号密码模式。
2. 仅存在 `token` 时，按旧逻辑转发 token。
3. 两种凭据都没有时，MCP 返回 `401`。

## 4. 基本使用方式

在 Codex 中直接用自然语言描述即可，建议明确环境名称。

示例：

```text
用 zentao 查询产品列表
```

```text
用 zentao 查询“陈鹏列”五月份提交了哪些 bug
```

```text
用 zentao 查询产品 ID 为 654 的 Bug 列表，只读
```

```text
用 zentao 创建一个任务，所属执行 4058，任务名是“禅道MCP测试”，指派给陈鹏列，预计工时 5
```

### 4.1 扩展工具（查 Bug / AI 评分 / 问题分析）

除了接口级工具，服务端还提供一组只读的 `zentao_*` 复合工具，把多次调用合并成一次问答：

| 工具 | 用途 |
|---|---|
| `zentao_find_similar_bugs` | 根据现场现象跨产品查历史 Bug，返回候选及真实修复过程；需要索引已启用 |
| `zentao_resolve_scope` | 按名字找产品、项目、执行、人员的 ID |
| `zentao_search_bugs` | 跨范围查 Bug，支持关键字、责任人、日期、严重程度过滤，并返回分布统计 |
| `zentao_analyze_bug` | 单个 Bug 的问题分析：时间线、备注、修复时长、重新激活次数、相似 Bug |
| `zentao_ai_score` | 查任务/Bug/需求的 AI 评分，含每条备注的评分和覆盖率 |
| `zentao_quality_report` | 某产品/迭代的 Bug 质量统计与逐月趋势 |
| `zentao_user_worklog` | 某人某时间段创建/完成/解决了哪些记录 |

自然语言示例：

```text
用 zentao 查一下名字里带“数据中心”的产品 ID
```

```text
用 zentao 查产品 654 里 2026 年 5 月 chenpenglie 提的、标题带“导出”的 Bug
```

```text
用 zentao 分析 Bug 88123 的原因，并列出同类问题
```

```text
用 zentao 看看执行 4058 最近 20 个任务的 AI 评分情况
```

需要在 `config.yaml` 中为该 server 打开 `zentao_extensions: true`。
完整参数、返回结构和限制见 [zentao-mcp-extensions.md](zentao-mcp-extensions.md)。

## 5. 常用查询

### 查询产品列表

常用参数：

| 参数 | 说明 |
|---|---|
| `browseType` | `all` 全部，`noclosed` 未关闭，`closed` 已关闭 |
| `limit` | 每页数量 |
| `page` | 页码 |
| `orderBy` | 例如 `id_desc`、`title_asc` |

自然语言示例：

```text
用 zentao 查询前 10 个产品，按 ID 倒序
```

### 查询产品 Bug 列表

Bug 列表通常需要产品 ID。

常用参数：

| 参数 | 说明 |
|---|---|
| `productID` | 产品 ID |
| `status` | v1 推荐使用 `all` 查询全部 |
| `browseType` | `all`、`unclosed`、`openedbyme`、`assignedtome` 等 |
| `limit` | 每页数量 |
| `page` | 页码 |
| `orderBy` | 例如 `id_desc`、`status_desc` |

自然语言示例：

```text
用 zentao 查询产品 654 下 2026 年 5 月由 chenpenglie 创建的 Bug
```

### 查询执行任务列表

任务列表通常需要执行 ID。

自然语言示例：

```text
用 zentao 查询执行 4058 的任务列表，按 ID 倒序
```

## 6. 创建任务

创建任务至少需要：

| 字段 | 说明 |
|---|---|
| 所属执行 ID | 任务必须挂到某个执行/迭代下 |
| 任务名称 | 任务标题 |
| 指派人 | 建议使用账号，例如 `chenpenglie` |

常用可选字段：

| 字段 | 说明 |
|---|---|
| 描述 | 任务描述 |
| 预计工时 | `estimate` |
| 剩余工时 | `left` |
| 任务类型 | 例如 `devel`、`test` |
| 预计开始 | `YYYY-MM-DD` |
| 截止日期 | `YYYY-MM-DD` |

自然语言示例：

```text
用 zentao 在执行 4058 下创建任务：
任务名称：禅道MCP测试。
描述：测试MCP功能是否生效
指派给：陈鹏列
预计工时：5
任务类型：test
```

注意：

- “执行/迭代 ID”和“任务 ID”不是同一个概念。
- 例如 `83000` 如果查询出来是任务详情，就不能当作执行 ID 使用。
- 创建前最好先确认执行是否正确，避免任务挂错位置。

## 7. 创建 Bug

创建 Bug 通常需要：

| 字段 | 说明 |
|---|---|
| `product` | 所属产品 ID。企业版 12.3 v1 创建 Bug 使用 `product`，不是 `productID` |
| `title` | Bug 标题 |
| `openedBuild` | 影响版本，主干通常是 `trunk` |

自然语言示例：

```text
用 zentao 在产品 654 下创建 Bug：
标题：页面保存后状态未刷新
影响版本：trunk
严重程度：3
优先级：3
重现步骤：进入页面，点击保存，观察状态没有刷新
```

## 8. 使用建议

1. 查询类操作建议明确说 `zentao` 或 `zentao-sandbox`。
2. 写操作建议先在 `zentao-sandbox`（测试库）验证，再对 `zentao` 操作。注意 `zentao` 是**正式库**，写进去就是真数据。
3. 写操作前尽量提供完整上下文，例如产品 ID、执行 ID、任务名、指派人、日期。
4. 指派人建议使用禅道账号而不是姓名，例如 `chenpenglie`。
5. 不要把禅道密码写进项目文件、Markdown 文档或代码仓库。

## 9. 常见问题

### 9.1 MCP 工具没有出现

检查 Codex 配置中是否存在：

```toml
[mcp_servers.zentao]
enabled = true
url = "http://10.70.2.73:7892/zentao/mcp"
```

修改配置或环境变量后，需要重启 Codex。

如果工具曾经正常、现在整体消失，多半是还在用已废弃的 `zentao-test` / `zentao-prod`
地址（服务端已不再提供），按第 1 节的新地址更新即可。

### 9.2 返回 `HTTP 401 Unauthorized`

常见原因：

1. `ZENTAO_ACCOUNT` 或 `ZENTAO_PASSWORD` 没有设置。
2. Codex 未重启，没有读取到新的环境变量。
3. 账号密码无法登录对应禅道环境。
4. 使用旧 token 模式时，`ZENTAO_TOKEN` 已过期。

账号密码模式下，MCP 服务端会自动通过禅道 v1 token 接口登录，不需要用户手动刷新 token。

### 9.2.1 公网 `403`、登录 `429` 或相似检索没有出现

- 公网 `403`：先检查 `X-Mcp-Key`；密钥缺失或错误时请求还没有到 MCP。
- 带入口密钥但 `401 / no OAuth metadata found`：检查个人禅道 Header 是否发送。本服务使用 Header 认证，不能用 OAuth 登录补救缺少的 Header。
- 工具错误包含登录保护 `429`：同一账号已经达到失败上限。停止重试，核对正式/测试地址和密码，按错误中的 `retryAfterSeconds` 等待；改不同的错误密码不会绕过保护。
- 没有 `zentao_find_similar_bugs`：维护者检查索引开关、服务账号和容器内 `ZENTAO_INDEX_PASSWORD` 是否非空。
- 工具存在但提示“首次构建”：等待索引完成；“构建失败”应查日志和权限，不能反复请求期待自行变好。
- `index.partial=true`：本次索引有截断或读取失败；不能把“未命中”当作历史上没有此问题。即使 `partial=false`，索引也只覆盖服务账号可见范围。

### 9.3 输出 schema 校验失败

禅道 v1 实际返回字段类型有时和 OpenAPI 文档不一致，例如数字可能返回字符串、对象可能返回空字符串。服务端已做兼容处理；如果仍遇到类似：

```text
validating tool output
oneOf
```

需要继续修正 OpenAPI schema 或 MCP 输出兼容逻辑。

### 9.4 创建任务接口异常

如果创建任务返回 PHP fatal 或参数数量错误，通常表示 OpenAPI 文档中的路径和真实 v1 接口不一致。

真实创建任务接口应挂在具体执行下：

```text
POST /executions/{executionID}/tasks
```

随附的 OpenAPI 文档已经把 `POST /tasks` 等 12 个 12.3 v1 不支持的路由标记为 `deprecated`。
在 `config.yaml` 中开启 `skip_deprecated: true` 后，这些工具不会再注册，模型也就不会选到它们。

## 10. 维护者说明

服务端 token 管理逻辑：

1. 客户端传入 `zentao-account` 和 `zentao-password`。
2. MCP 服务端按 `base_url + account` 缓存 token，核对密码摘要，默认有效期 24 小时。
3. token 只存在内存，不落盘。
4. 首次请求自动登录禅道。
5. 上游返回 `401` 时合并同一账号的并发刷新，重新登录并重试一次；不同账号不互相等待网络登录。
6. 重试仍失败时返回上游错误。
7. 按凭据退避保留，同时默认同一账号累计两次登录拒绝后暂停新的登录；从最后一次实际拒绝起冷却 10 分钟。有效缓存会话仍可用，错误密码不清除正常会话。
8. token、凭据失败和账号保护缓存分别最多 2,048 条。查询时清理过期项；token 按最近使用淘汰，活动账号保护桶不淘汰，满时拒绝新账号登录。凭据失败默认保留 30 分钟。

可在 server 下配置 `auth` 的 `failure_limit`、`failure_window`、`token_ttl`、`failure_ttl`、`max_cache_entries`。失败阈值应低于禅道真实锁号阈值；保护按进程生效，不等于禅道所有入口共享的全局锁号策略。

`config.yaml` 改动需要重启进程；`.env` 改动需要重新创建容器：

```bash
docker compose up -d --force-recreate zentao-mcp
```

`docker compose restart` 不会重新注入环境变量。云上完整发布与回滚见 [部署说明](../deploy/cloud/README.md)。

日志脱敏字段包括：

```text
token
authorization
password
zentao-password
zentao_password
```

部署命令：

```bash
cd /home/chenpenglie/AI/zentao-mcp
./docker-compose up -d --build
```

健康检查：

```bash
docker inspect -f '{{.State.Health.Status}}' zentao-mcp
```
