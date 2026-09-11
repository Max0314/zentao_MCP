# 禅道 MCP 使用说明

本文说明如何在 Codex 中使用禅道 MCP，覆盖测试环境、正式环境、账号密码认证、常用操作示例和常见问题排查。

## 1. 环境说明

当前 MCP 服务统一部署在：

```text
http://10.70.2.73:7892
```

已配置两个禅道环境：

| MCP 名称 | MCP 地址 | 上游禅道环境 |
|---|---|---|
| `zentao-test` | `http://10.70.2.73:7892/zentao-test/mcp` | `http://10.70.33.19/biz/api.php/v1` |
| `zentao-prod` | `http://10.70.2.73:7892/zentao-prod/mcp` | `http://10.70.33.18/biz/api.php/v1` |

在 Codex 中加载后，对应工具命名空间通常显示为：

```text
mcp__zentao_test
mcp__zentao_prod
```

日常使用时不需要直接写工具名，直接用自然语言说明环境和操作即可。

## 2. Codex 配置

推荐使用账号密码模式，由 MCP 服务端自动登录禅道、缓存 token、在 token 失效后自动刷新。

```toml
[mcp_servers.zentao-test]
enabled = true
url = "http://10.70.2.73:7892/zentao-test/mcp"
env_http_headers = { "zentao-account" = "ZENTAO_ACCOUNT", "zentao-password" = "ZENTAO_PASSWORD" }

[mcp_servers.zentao-prod]
enabled = true
url = "http://10.70.2.73:7892/zentao-prod/mcp"
env_http_headers = { "zentao-account" = "ZENTAO_ACCOUNT", "zentao-password" = "ZENTAO_PASSWORD" }
```

本机需要设置两个环境变量：

```powershell
[Environment]::SetEnvironmentVariable("ZENTAO_ACCOUNT", "你的禅道账号", "User")
[Environment]::SetEnvironmentVariable("ZENTAO_PASSWORD", "你的禅道密码", "User")
```

设置后重启 Codex，让 Codex 重新读取环境变量和 MCP 配置。

## 3. 兼容旧 Token 模式

旧的 token header 方式仍然保留，可用于临时兼容历史客户端。

```toml
[mcp_servers.zentao-test]
enabled = true
url = "http://10.70.2.73:7892/zentao-test/mcp"
env_http_headers = { token = "ZENTAO_TEST_TOKEN" }
```

认证优先级：

1. 同时存在 `zentao-account` / `zentao-password` 和 `token` 时，优先使用账号密码模式。
2. 仅存在 `token` 时，按旧逻辑转发 token。
3. 两种凭据都没有时，MCP 返回 `401`。

## 4. 基本使用方式

在 Codex 中直接用自然语言描述即可，建议明确环境名称。

示例：

```text
用 zentao-test 查询产品列表
```

```text
用 zentao-test 查询“陈鹏列”五月份提交了哪些 bug
```

```text
用 zentao-prod 查询产品 ID 为 654 的 Bug 列表，只读
```

```text
用 zentao-test 创建一个任务，所属执行 4058，任务名是“禅道MCP测试”，指派给陈鹏列，预计工时 5
```

### 4.1 扩展工具（查 Bug / AI 评分 / 问题分析）

除了接口级工具，服务端还提供一组只读的 `zentao_*` 复合工具，把多次调用合并成一次问答：

| 工具 | 用途 |
|---|---|
| `zentao_resolve_scope` | 按名字找产品、项目、执行、人员的 ID |
| `zentao_search_bugs` | 跨范围查 Bug，支持关键字、责任人、日期、严重程度过滤，并返回分布统计 |
| `zentao_analyze_bug` | 单个 Bug 的问题分析：时间线、备注、修复时长、重新激活次数、相似 Bug |
| `zentao_ai_score` | 查任务/Bug/需求的 AI 评分，含每条备注的评分和覆盖率 |
| `zentao_quality_report` | 某产品/迭代的 Bug 质量统计与逐月趋势 |
| `zentao_user_worklog` | 某人某时间段创建/完成/解决了哪些记录 |

自然语言示例：

```text
用 zentao-test 查一下名字里带“数据中心”的产品 ID
```

```text
用 zentao-test 查产品 654 里 2026 年 5 月 chenpenglie 提的、标题带“导出”的 Bug
```

```text
用 zentao-test 分析 Bug 88123 的原因，并列出同类问题
```

```text
用 zentao-test 看看执行 4058 最近 20 个任务的 AI 评分情况
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
用 zentao-test 查询前 10 个产品，按 ID 倒序
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
用 zentao-test 查询产品 654 下 2026 年 5 月由 chenpenglie 创建的 Bug
```

### 查询执行任务列表

任务列表通常需要执行 ID。

自然语言示例：

```text
用 zentao-test 查询执行 4058 的任务列表，按 ID 倒序
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
用 zentao-test 在执行 4058 下创建任务：
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
用 zentao-test 在产品 654 下创建 Bug：
标题：页面保存后状态未刷新
影响版本：trunk
严重程度：3
优先级：3
重现步骤：进入页面，点击保存，观察状态没有刷新
```

## 8. 使用建议

1. 查询类操作建议明确说 `zentao-test` 或 `zentao-prod`。
2. 写操作建议先在 `zentao-test` 验证，再对 `zentao-prod` 操作。
3. 写操作前尽量提供完整上下文，例如产品 ID、执行 ID、任务名、指派人、日期。
4. 指派人建议使用禅道账号而不是姓名，例如 `chenpenglie`。
5. 不要把禅道密码写进项目文件、Markdown 文档或代码仓库。

## 9. 常见问题

### 9.1 MCP 工具没有出现

检查 Codex 配置中是否存在：

```toml
[mcp_servers.zentao-test]
enabled = true
url = "http://10.70.2.73:7892/zentao-test/mcp"
```

修改配置或环境变量后，需要重启 Codex。

### 9.2 返回 `HTTP 401 Unauthorized`

常见原因：

1. `ZENTAO_ACCOUNT` 或 `ZENTAO_PASSWORD` 没有设置。
2. Codex 未重启，没有读取到新的环境变量。
3. 账号密码无法登录对应禅道环境。
4. 使用旧 token 模式时，`ZENTAO_TEST_TOKEN` 已过期。

账号密码模式下，MCP 服务端会自动通过禅道 v1 token 接口登录，不需要用户手动刷新 token。

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
2. MCP 服务端按 `base_url + account` 缓存 token。
3. token 只存在内存，不落盘。
4. 首次请求自动登录禅道。
5. 上游返回 `401` 时清除缓存，重新登录并重试一次。
6. 重试仍失败时返回上游错误。

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
