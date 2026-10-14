# 云上部署（NeoFlow 主服务器）

把禅道 MCP 从内网搬到公有云的部署说明。内网那套（10.70.2.73）不受影响，继续保留。

## 一、两套的区别

|  | 内网 10.70.2.73 | 云上 47.108.48.50 |
|---|---|---|
| 对外地址 | `http://10.70.2.73:7892/zentao/mcp` | `https://neoflow-cn.neo-net.com/zentao-mcp/mcp` |
| 监听 | `0.0.0.0:7892` | `127.0.0.1:39029`，nginx 反代 |
| `base_url` | `http://10.70.33.19/biz/api.php/v1` | `https://itms.changhongnetwork.net:28443/biz/api.php/v1` |
| server `name` | `zentao` | `zentao-mcp`（必须等于 nginx 子路径） |
| sandbox 条目 | 保留 | 不配（10.70.33.18 无公网入口） |

同一份仓库代码。差异全部落在两处**不进 git** 的本机文件上：`config.yaml` 和 `.env`。
`git archive` 只发布被跟踪的文件，不会覆盖它们。

## 二、前置条件

1. 47.108.48.50 上已有 SSH 账号，且本机公钥已加入（见《NeoFlow 服务器使用与维护说明》）。
2. 已有索引服务账号 `mcpread`。先验证登录与产品可见范围，补齐所需历史产品的只读权限。只在迭代里加成员不一定覆盖全部产品。
3. nginx 配置就位后执行 `nginx -t` 并 reload。账号失败保护在 Go 服务内生效；来源 IP 限流是可选的流量保护，不能替代账号保护。共享出口的多人客户端要按实际流量定参数。
4. 这台机器给本服务留 1.5~2GB 可用内存（索引常驻约 380MB，建索引时另有峰值）。

## 三、部署步骤

以下发布命令从本地 Git Bash 执行；使用已经推送到 GitHub 的提交。

```bash
# 1. 发布代码（只发布被跟踪的文件，不会覆盖服务器上的 config.yaml / .env）
git archive --format=tar HEAD | ssh -i /d/id_ed25519_max -p 39999 max@47.108.48.50 \
  'mkdir -p ~/zentao-mcp && tar -x -C ~/zentao-mcp'

# 2. 首次部署：在服务器上准备两个本机文件
ssh -i /d/id_ed25519_max -p 39999 max@47.108.48.50
cd ~/zentao-mcp
cp deploy/cloud/config.example.yaml config.yaml   # 按注释改 account
cp deploy/cloud/env.example .env                  # 填 ZENTAO_INDEX_PASSWORD
chmod 600 .env

# 3. 先构建再替换，编译失败时当前容器不受影响
MCP_REVISION="<已推送的提交SHA>" docker compose build && docker compose up -d

# 4. nginx 子路径反代
cp deploy/cloud/zentao-mcp.nginx.conf ~/.nginx/zentao-mcp.conf
openssl rand -hex 32          # 把输出填进那个文件的 X-Mcp-Key 占位符
# 然后执行 sudo nginx -t && sudo systemctl reload nginx
```

只改了 `config.yaml` 的话用 `docker compose restart`：`up -d` 看不到 spec 变化，
会让容器带着旧配置继续跑。

改 `.env` 后必须执行 `docker compose up -d --force-recreate zentao-mcp`，
`restart` 不会把新的 `ZENTAO_INDEX_PASSWORD` 注入已有容器。
`.env` 中有 `$`、`#` 等字符的密码使用单引号包裹，保留字面值；不要打印
`docker compose config` 或容器的完整环境变量，这些输出包含真实口令。

首次部署用上述复制模板步骤；后续发布保留现有 `config.yaml`、`.env` 和 nginx 密钥。
发布前先核对运行目录的源码是否与上次发布一致，再备份本机配置；只发布已提交、
已推送版本的 `git archive`，不要打包整个工作目录。SSH 使用 39999 端口；
Windows PowerShell 的密钥路径为 `D:\id_ed25519_max`，Git Bash 才使用 `/d/id_ed25519_max`。
`.dockerignore` 排除 `.env`、运行配置和备份，防止它们进入镜像的构建层。

## 四、验证

```bash
# 链路通不通
curl -s -o /dev/null -w "%{http_code}\n" -X POST \
  -H "X-Mcp-Key: <你的key>" \
  https://neoflow-cn.neo-net.com/zentao-mcp/mcp
```

- `401` —— 通了。这个 401 来自服务端的「缺认证头」分支，说明请求已经穿过 nginx 打到进程里。
- `403` —— key 不对。
- `404` —— nginx 子路径和 `config.yaml` 的 `name` 没对上。
- `502` —— `.env` 的 `MCP_PORT` 和 nginx `proxy_pass` 的端口对不上（容器内端口固定 7892，不用管）。

再看启动日志确认索引在建：

```bash
docker compose logs --tail=50 zentao-mcp | grep -E 'server registered|bug index'
```

预期看到 `server registered name=zentao-mcp` 和 `bug index builder started`。
用 `docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' zentao-mcp` 核对运行镜像的提交 SHA。
全量建索引的请求数取决于可见产品和分页，分钟级完成。

首次构建期间工具会明确返回“索引正在首次构建”，不是空结果。
还要确认 `bug index built` 的 `failed_products=0`、`truncated_products=0`、
`product_list_truncated=false`。固定 50 页上限已经移除，实际抓取上限由
`max_products` / `max_bugs_per_product` 决定；满页正好等于总数不算截断，
重复页无新 ID 时会停止并报告分页异常。

网络 `401` 只证明链路通，不能证明账号、索引与检索正常。验收还包括：

1. 使用真实用户 Header 完成 MCP `initialize` 和 `tools/list`，确认 7 个 `zentao_*` 复合工具，包括 `zentao_find_similar_bugs`。
2. 执行一次历史现象查询，再对命中 Bug 调 `zentao_analyze_bug`，确认详情和备注按调用者权限回查。
3. 观察 `index.partial`、`index.lastRefreshFailed` 和 `notes`；不完整索引的“未命中”不能证明历史不存在。
4. SSE 握手的 endpoint 必须包含 `/zentao-mcp/sse?sessionid=...`；入口不带 `X-Mcp-Key` 应为 `403`。

2026-10-14 配置检查：`mcpread` 可见 421 个产品，内网索引账号可见 625 个，
差 204 个产品。当前云上可以检索其已有权限范围；补齐产品权限后，重启或等下一次
刷新重建索引。权限扩大不是代码或分页修复能代替的。

## 四点一、登录保护与缓存

默认保留按凭据的 15 秒至 10 分钟指数退避，并增加按账号的失败预算：
两次实际登录被拒绝后，停止该账号的新登录，从最后一次拒绝起冷却 10 分钟。
受保护请求不会到禅道，也不会延长冷却；改不同密码不能绕过。有效 token 会话仍可用。
阈值必须低于禅道实际锁号阈值，可在 server 配置下调整：

```yaml
auth:
  failure_limit: 2
  failure_window: "10m"
  token_ttl: "24h"
  failure_ttl: "30m"
  max_cache_entries: 2048
```

每类缓存都有容量上限与过期清理。token 按最近使用淘汰；活动账号保护桶不被淘汰，
容量满时新账号登录暂时返回保护错误。不同账号的登录并行，同一账号的刷新合并，
登录网络请求最多 30 秒。保护按单个进程生效；内外两套不共享计数。

## 五、回滚

```bash
git archive --format=tar <上一个 commit> | ssh -i /d/id_ed25519_max -p 39999 max@47.108.48.50 \
  'tar -x -C ~/zentao-mcp'
ssh -i /d/id_ed25519_max -p 39999 max@47.108.48.50 'cd ~/zentao-mcp && MCP_REVISION="<上一个提交SHA>" docker compose build && docker compose up -d'
```

nginx 侧回滚先停用或恢复该服务自己的配置文件，执行 `sudo nginx -t` 后才 reload。
代码回滚保留现有凭据；若同时回滚 `.env`，恢复权限 600 的备份并重新创建容器。

## 六、这台机器上会存什么

不用数据库。MySQL / PostgreSQL / Redis 一个都不碰，不需要 NeoFlowData 的账号。

| 位置 | 内容 | 重启后 |
|---|---|---|
| 内存 | Bug 索引约 380MB（标题 + 每条 400 字现象） | 丢失，重建约分钟级 |
| 内存 | token 缓存：禅道 token 明文 + 口令的 SHA-256 摘要（不是口令本身） | 丢失 |
| 磁盘·只读 | `config.yaml`、`docs/zentao-openapi.json` | — |
| 磁盘·可写 | `.env`（索引账号口令，600） | — |
| 磁盘·可写 | docker 容器日志 | — |

容器日志里有什么：访问日志记方法、路径、查询参数（敏感键已脱敏）、来源 IP、UA、
状态码、耗时，**不记请求头也不记请求体**，所以用户的禅道口令不会进日志；工具调用
日志记**完整入参**，只对 password/token 这类键名脱敏，也就是说建 Bug 的标题正文、
检索关键词会落到日志文件里。`docker-compose.yml` 里已配 50MB × 5 轮转。

需要留意的是：开启 `bug_index` 意味着禅道缺陷库的标题和现象描述会常驻在这台公网
机器的内存里，并按 `refresh` 周期重建。这属于数据出内网，需要安全口认可；不认可
就把 `bug_index.enabled` 设为 `false`，相似问题检索继续用内网那套。
