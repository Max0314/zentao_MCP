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
2. 禅道里已新建只读服务账号（建议 `mcp-readonly`），可见产品范围尽量与 admin 一致。
3. 需要维护者配合两件事（都只改一行）：
   - 在 nginx 主配置的 http 段加 `limit_req_zone $binary_remote_addr zone=mcp:10m rate=10r/s;`
   - 配置就位后执行 `nginx -t` 并 reload。
4. 这台机器给本服务留 1.5~2GB 可用内存（索引常驻约 380MB，建索引时另有峰值）。

## 三、部署步骤

```bash
# 1. 发布代码（只发布被跟踪的文件，不会覆盖服务器上的 config.yaml / .env）
git archive --format=tar HEAD | ssh <username>@47.108.48.50 \
  'mkdir -p ~/zentao-mcp && tar -x -C ~/zentao-mcp'

# 2. 首次部署：在服务器上准备两个本机文件
ssh <username>@47.108.48.50
cd ~/zentao-mcp
cp deploy/cloud/config.example.yaml config.yaml   # 按注释改 account
cp deploy/cloud/env.example .env                  # 填 ZENTAO_INDEX_PASSWORD
chmod 600 .env

# 3. 先构建再替换，编译失败时当前容器不受影响
docker compose build && docker compose up -d

# 4. nginx 子路径反代
cp deploy/cloud/zentao-mcp.nginx.conf ~/.nginx/zentao-mcp.conf
openssl rand -hex 32          # 把输出填进那个文件的 X-Mcp-Key 占位符
# 然后请维护者 nginx -t && systemctl reload nginx
```

只改了 `config.yaml` 的话用 `docker compose restart`：`up -d` 看不到 spec 变化，
会让容器带着旧配置继续跑。

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
全量建索引约 1300 次请求，分钟级完成；这期间相似问题检索会返回空。

## 五、回滚

```bash
git archive --format=tar <上一个 commit> | ssh <username>@47.108.48.50 \
  'tar -x -C ~/zentao-mcp'
ssh <username>@47.108.48.50 'cd ~/zentao-mcp && docker compose build && docker compose up -d'
```

nginx 侧回滚就是删掉 `~/.nginx/zentao-mcp.conf` 再请维护者 reload。

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
