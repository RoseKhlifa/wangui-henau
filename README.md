# wangui

> 学校晚归签到自动化系统 —— 内部自用、邀请制、≤5 人 + 偶尔几个一次性朋友。
> **不对外开放，不对外宣传，不接受陌生人申请。**

---

## ⚠️ 风险与边界

这个工具替你和你的朋友**在每天 22:00–22:30 期间自动调用学校签到接口**。它的存在前提是：

1. **你确实在校** —— 不在校时让脚本签到 = 谎报位置 = 违纪。后果由用户承担
2. **token 是用户自己的** —— 工具不会盗号，也不读其他人的信息
3. **仅供有限熟人** —— 不公开、不收费、不传播
4. **学校发现就关停** —— 学校发文要求停止此类工具时，立即停服

这是一个**为方便而存在的灰色工具**，不是一个产品。仓库私有就是这个原因。

---

## 0. 这是什么

```
┌────────────────────────────────────────────────────────────────┐
│                                                                 │
│   学校晚归签到系统 (xhbcs.henau.edu.cn)                          │
│      ▲                       ▲                                  │
│      │ JWT Bearer            │ OAuth2 微信扫码                  │
│      │ /checkin/*            │ 复制回调链接                     │
│  ┌───┴──────────────────────┴───┐                               │
│  │  wangui server (Go + Vue)    │  ←──  朋友浏览器               │
│  │  Docker on VPS               │       (用户 SPA + 激活页)      │
│  └────────┬─────────────────────┘                               │
│           │                                                      │
│           │ 每天 22:00–22:10                                     │
│           │ 替每个用户调签到 API                                 │
│           ▼                                                      │
│   ┌──────────┐  ┌──────────┐  ┌──────────┐                      │
│   │  邮件     │  │ Server 酱 │  │  SSE 实时 │  → admin / 用户    │
│   │ (SMTP)   │  │ (微信推送) │  │  事件流   │                    │
│   └──────────┘  └──────────┘  └──────────┘                      │
│                                                                  │
└────────────────────────────────────────────────────────────────┘
              │
              │ 备用通道（罕用，扫码异常时兜底）
              ▼
   wangui.exe (Wails GUI) —— 本地 MITM 抓 token
```

两个独立子项目：

| 子项目 | 路径 | 跑哪 | 作用 |
|---|---|---|---|
| **wangui server** | `cmd/wangui/` `internal/` `web/` | Linux VPS (Docker) | 多租户后端 + 用户 SPA + 管理员后台 + 微信 OAuth 换 token |
| **wangui.exe (tokengrab)** | `tokengrab/` | 用户 Windows | Wails GUI，本地 MITM 抓 JWT。**备用通道** —— 扫码不可用时才需要 |

---

## 1. 主要功能

### 用户侧

- **两步式激活**：先填邀请码 + PIN + 免责（公开），通过后才显示微信扫码 OAuth 界面（路人看不到 OAuth UI）
- **微信扫码登录学校** → 复制回调链接 → wangui 后端 OAuth code 换 token（不需要装任何客户端）
- **Token 自动续期**：用户在「账号」页扫码 / 粘链接 / 粘 JWT 都可（多通道兜底）
- **个人签到时刻**：每个用户激活时随机分配 0–9 分钟 trigger + 60s jitter，全部在 22:00–22:10 内完成（万一系统出错，留 20 分钟手动补救窗口）
- **自选签到日期**：周几掩码（每天 / 工作日 / 周末 / 自定义）
- **Dashboard 数据卡**：连签天数 (🔥 ≥30 焰色) / 本月进度条 / 总签到次数 / 历史最长记录，全部 records 实时算
- **今日签到状态机**：今日休息 / 已完成 / 免签(请假) / 失败 / 已错过窗口 / 正在尝试 / 即将开始 / 待签到，22:00 窗口期内每 30s 自动重拉，无需刷新
- **两个通知通道**（独立可启）：
  - **邮件**：填邮箱 + 开关，签到结果 + Token 剩 2 天提醒
  - **Server 酱（方糖）**：粘 SendKey + 开关，事件直接推到微信。FAQ + 测试按钮一气呵成
- **通知通道状态 chip**：Dashboard hero 即时反映「微信 + 邮件 / 仅邮件 / 通知未配置」

### 管理员侧

- **概览**：用户数 / 邀请码 / 今日签到 / Token 24h 内过期告警 + **学校签到规则面板**（每日 18:00 自动从学校 API 抓取，规则变化自动推送）
- **监控看板**（`/rosekhlifa/monitor`）：
  - **全员可见**，6 类 chip 自动分类：今晚要签 / 临时·今晚 / 周次跳过 / 自动签关 / 已禁用 / 临时·非今天
  - 顶部 filter 按钮组 + 数量徽章
  - **SSE 实时事件流**：连接成功后表头变绿，相关行收到事件 4 秒绿色闪烁
  - 60s 兜底轮询，22:00 关键窗口期信息密度最高
  - 学校 `/checkin/status` 只对「今晚要签」分类拉取（省 API）
- **用户管理**（卡片 / 列表双视图，状态持久化）：
  - **卡片态**：每张卡聚合头像/姓名/学号/班级/邀请码 + 宿舍楼下拉 + autoSign toggle + signDays 7-bit 可点 + 22:0X 签到时刻 + 学校今日状态 + Token chip + 最近 3 条记录 + 立即签到/PIN/禁用/删除
  - **列表态**：7 列紧凑表格，点行 → 抽屉滑出 UserCard 完整内容；行首 checkbox 批量勾选 → 浮出操作栏「立即签 / 启用 / 禁用 / 删除」
  - **代刷 Token**：每个用户都有「刷新」按钮，让朋友重扫即可（强制学号匹配防覆盖错账号）
  - **代签到**：admin 一键替任何人签到，记录学校当前实际生效的 rule ID
- **临时朋友 (Guest 模式)**：admin 后台代为创建，按具体日期签到，到期自动 cleanup —— 给不熟的朋友 / 一次性场景。卡片有同款全功能（包括代刷 Token）
- **宿舍楼**：管理员维护可选签到位置；详细地址是新版加密签到协议的必填项
- **CSV 导出**：admin → 日志 → 选起止日期 → UTF-8+BOM 的 CSV，Excel 双击不乱码，含学号 + 姓名 + 时间 + 状态 + message
- **每日自动备份** + 7 份滚动保留 / **手动备份**：`docker compose exec wangui /usr/local/bin/wangui backup-now -data /data`
- **Server 酱 admin 通道**：等同微信版 BCC，所有用户（含临时朋友）的事件你都能收到

### 后台调度

- **每天 22:00** 触发签到窗口；每用户独立 goroutine，22:00-22:10 内按 trigger_minute 启动 + jitter + 重试
- **每天 10:00** Token 到期扫描：剩 < 48h 且本 token 周期未提醒过 → 邮件 + Server 酱告警 admin 与用户
- **每天 18:00** 拉一次学校 `available-rules`，diff 入库；首次入库不通知，第二次起规则变化时通知 admin
- **每天 02:00** 清理过期 Guest，邮件给 admin 一份摘要
- 每次签到先读取当前可用规则，再获取一次性 `prepare` 参数并提交加密载荷；提交后复核学校状态

### 数据安全

- SQLite WAL + AES-256-GCM 加密 token / SMTP 密码 / **Server 酱 admin SendKey**
- 用户自己的 Server 酱 SendKey 与 token 在同一表行（不再单独加密；威胁模型一致）
- DPAPI / OS 钥匙库未用 —— 主密钥在 `.env`
- schema idempotent 自动迁移；新字段加列不丢老数据

### wangui.exe (tokengrab GUI) —— 备用通道

- 一键 MITM 抓学校 JWT（不再是主流程，仅备用）
- 持久根 CA + DPAPI 加密私钥（**首启一次** Windows 安全确认）
- 抓到自动复制剪贴板 + 跳 wangui 激活页
- 详见 [`tokengrab/README.md`](./tokengrab/README.md) + [`tokengrab/PROJECT.md`](./tokengrab/PROJECT.md)（原理逆向）

---

## 2. 目录结构

```
.
├── README.md                  ← 你正在看
├── ubuntu.md                  服务器部署完整流程
├── func.md                    调度器细节 + 个人签到时刻设计
├── final.md / fuck.md         开发笔记
│
├── go.mod / go.sum            主项目依赖
├── Dockerfile                 多阶段：Node SPA → Go binary → Alpine
├── docker-compose.yml         端口 / volume / .env
├── .env.example .dockerignore .gitignore
│
├── cmd/wangui/                main + go:embed SPA + events.Bus 装配
│
├── internal/                  Go 业务代码
│   ├── api/                   学校 API client（GetUser / OAuth2Login / CheckinStatus / Sign / AvailableRules）
│   ├── backup/                VACUUM INTO 每日 + 7 份保留
│   ├── events/                in-mem pubsub 事件总线（best-effort）
│   ├── notify/                邮件 + Server 酱 dispatcher（user / admin / guest cleanup / token-warn / rules-changed）
│   ├── scheduler/             调度器 + 三个 daily ticker（guest-cleanup / token-warn / rules-watch）
│   ├── store/                 SQLite + 加密；users / dorms / codes / records / sessions / system_config
│   └── web/                   handlers / admin_handlers / school_oauth / stats / sse / server / rate limit
│
├── web/                       Vue 3 + TS + Tailwind 4
│   └── src/
│       ├── views/             Login / Dashboard / Records / Account / Settings (含 Server 酱卡片)
│       │   └── admin/         Dashboard / Monitor (实时看板) / Users (卡片+列表) / Guests /
│       │                       Codes / Dorms / Logs (含 CSV 导出) / Settings (含 admin Server 酱)
│       └── components/admin/  UserCard.vue 复用组件（卡片态 + 列表态抽屉共用）
│
├── tokengrab/                 备用 Wails GUI（独立 go.mod）
│
└── scripts/
    └── redistribute_triggers.sql   存量用户 trigger_minute 打散的一次性 SQL
```

---

## 3. 快速开始

### 3.1 服务器部署（首次）

**前提**：Ubuntu 22.04+ VPS，2 GB+ 内存（Docker 构建期需要），1Panel 已装好且 80/443 反代可用。

```bash
git clone git@github.com:RoseKhlifa/wangui-henau.git /root/wangui
cd /root/wangui

cp .env.example .env
# 编辑 .env，填:
#   WANGUI_ADMIN_PASS    16+ 位随机字符
#   WANGUI_MASTER_KEY    openssl rand -hex 32 的输出（必须备份到密码管理器）
chmod 600 .env

docker compose up -d --build
docker compose logs -f wangui
```

期望日志（启动后陆续出现）：

```
store ready data_dir=/data
admin panel enabled
scheduler armed next_window_start=...
guest cleanup armed next=...
token-warn armed next=...
rules-watch armed next=...
http server listening addr=0.0.0.0:5555
backup armed next=...
```

完整步骤（含 1Panel 反代 / 子域名 / 备份策略 / 升级 / 故障排查）见 [`ubuntu.md`](./ubuntu.md)。

> 如果反代用 nginx，**记得 `proxy_buffering off;` 给 `/api/v1/rosekhlifa/events`**，否则 SSE 事件会卡在 nginx 缓冲。1Panel 默认 OK。

### 3.2 朋友使用流程（主路径：扫码）

发给朋友这几样：

1. **邀请码**（admin 后台「邀请码管理」生成）
2. **wangui 站点 URL**（你的部署地址）

朋友操作：

1. 浏览器打开 wangui 站点 → 「激活」tab
2. **Step 1**：填邀请码 + PIN + 同意免责 → 点「下一步：获取学校 Token」
3. 服务器 precheck 邀请码 → 通过 → 自动跳 Step 2 + 显示二维码
4. **Step 2**：用手机微信扫二维码 → 学校晚归页面登录 → 点页面右上「⋯」→ 「复制链接」
5. 把链接粘到 wangui 输入框 → 点「激活账号」
6. 进 wangui「配置」→ 选自己宿舍楼 → 开自动签到
7. 选填邮箱 / Server 酱 SendKey（任选其一或都开）
8. 每天 22:00–22:10 自动签，按通道收通知

> 备用：扫码失败（极少）→ 让 admin 编译 `wangui.exe`（[`tokengrab/`](./tokengrab/)）给朋友，本地 MITM 抓 token 后粘贴到 wangui 激活页

### 3.3 添加临时朋友（admin 代管）

不熟的朋友 / 一次性场景：

1. admin 后台 → 「临时朋友」→ 「+ 新增临时朋友」
2. modal 填：备注 + 签到日期（日历多选）+ 绑定宿舍楼
3. 弹出二维码 → admin **截图发给朋友**
4. 朋友微信扫码 → 学校登录 → 复制链接 → **发回 admin**
5. admin 把链接粘到 modal 的输入框 → 「创建临时朋友」
6. 朋友账号在选定的日期自动签到，最后一天结束后**次日 02:00 自动 cleanup**

朋友**完全不接触 wangui 网站**。Token 抓取 / 用户管理全由 admin 代理；Token 快过期时，admin 卡片上「刷新」按钮变红/琥珀，让朋友重扫一次就续。

### 3.4 22:00 监控（推荐工作流）

22:00 前打开 **admin → 监控看板**，标题旁「实时」chip 应是绿色。

- **22:00 前预览**：列表按 22:0X 时刻升序排好，每行学校状态实时拉
- **22:00–22:30 进行中**：行内 SSE 4 秒绿色闪烁 = 该用户刚出结果；status chip 自动变色
- **22:30 后战报**：顶部 5 个 tile 显示已签 / 待签 / 请假 / 异常的总数
- 异常用户在卡片上点「立即签」就能代签，记录仍保留学校当前实际规则 ID

---

## 4. 开发流程

```powershell
# 改代码 -- 任何编辑器

# 后端编译
go build ./...

# 前端类型检查
cd web ; npx vue-tsc --noEmit ; cd ..

# tokengrab
cd tokengrab ; go build ./... ; cd frontend ; npx vue-tsc --noEmit
```

推送 + 服务器部署：

```powershell
git add -A
git diff --cached --stat
git commit -m "feat: ..."
git push
```

```bash
ssh root@<服务器>
cd /root/wangui && git pull && docker compose up -d --build
docker compose logs -f wangui
```

DB schema 自动迁移，数据零丢失。

---

## 5. 环境变量

写在服务器 `/root/wangui/.env`（不进仓库）。

| 变量 | 必填 | 说明 |
|---|---|---|
| `WANGUI_ADMIN_PASS` | ✓ | 管理员后台登录密码，16+ 位随机字符 |
| `WANGUI_MASTER_KEY` | ✓ | AES-256 主密钥，`openssl rand -hex 32`；**丢了不可恢复，必须备份** |
| `TZ` | docker compose 已默认 | `Asia/Shanghai`，scheduler 用国内时区 |

---

## 6. 常用管理操作

### 生成邀请码（永久制）

admin → 「邀请码管理」→ 「生成邀请码」→ 数量 + 备注 → 弹 modal 复制。

### 添加临时朋友（一次性）

admin → 「临时朋友」→ 「+ 新增临时朋友」。详见 §3.3。

### 添加宿舍楼

admin → 「宿舍楼管理」→ 「添加宿舍楼」→ 起名 + 手动填 WGS84 经纬度 + 与坐标对应的详细地址。

> 推荐 **Bing Maps** (`bing.com/maps`) 切卫星 → 右键楼栋 → 弹出框显示 6 位 WGS84。
> **不要**用百度 / 高德 / 腾讯（GCJ02 加密，偏 100–500 米）。

### 配置 SMTP（邮件）

admin → 「系统设置」→ 「邮件通知」。Gmail 推荐：

| 字段 | 值 |
|---|---|
| Host | `smtp.gmail.com` |
| Port | `587` (STARTTLS) |
| 用户名 | Gmail 邮箱 |
| 应用专用密码 | Google 账号 → 安全性 → App Passwords（需先开两步验证） |
| 管理员收件邮箱 | 你自己，作为 BCC 收所有签到结果 + 临时朋友 cleanup + Token 告警 |

### 配置 Server 酱（管理员侧）

admin → 「系统设置」→ 「Server 酱 (管理员推送)」

1. 访问 `sct.ftqq.com` 微信扫码登录 → 复制 SendKey（形如 `SCT123...`，付费 `SCU` 也支持，Server 酱³ `sctp...` 自动走 push.ft07.com）
2. 粘到输入框 → 打开开关 → 保存配置 → 点「发测试推送」→ 微信收到即成功
3. **会推送**：每个用户（含临时朋友）的自动签到「最终结果」 + 任何用户 Token 剩 2 天内的告警 + 学校规则变化
4. 不推送：手动「立即签到」（避免刷屏）

用户自己的 Server 酱在 wangui 用户侧「设置」页配置，**独立于管理员**：用户的 key 只推他自己的事件。

### 用户管理（卡片 / 列表）

admin → 「用户」→ 右上角切换 `卡片 / 列表`。

- **卡片态**：信息密度低，操作友好
- **列表态**：信息密度高，**支持多选批量**（启用 / 禁用 / 立即签 / 删除），点行展开右侧抽屉 = 同款卡片

### 看签到日志 + 导出 CSV

admin → 「日志」→ 状态过滤 / 上 200 条；**导出 CSV** 选起止日期点下载，UTF-8 + BOM，Excel 双击。

### 强制重抓某人的 token

让用户进 wangui「账号」页 → 重新扫码 + 粘链接。会同时刷新头像。

**admin 代刷**：admin → 「用户」→ 找到他 → 卡片 Token chip 右侧「刷新」→ modal 出二维码截图发给他 → 朋友重扫复制回调 → 粘进去（学号必须匹配，防覆盖错账号）。

### 监控今晚

admin → 「监控看板」。22:00 前后整页 SSE 实时推，相关行 4 秒绿色闪烁。详见 §3.4。

### 手动备份

```bash
docker compose exec wangui /usr/local/bin/wangui backup-now -data /data
# 备份生成在 /root/wangui/data/backups/，rsync 拉到本地长期保留
```

### 重新打散存量用户的签到时刻

存量用户在 `trigger_minute=2` 升级前默认值 → 都集中在 22:02，看着像批量行为。一次性 SQL 重洗：

```bash
docker compose stop
sqlite3 data/wangui.db < scripts/redistribute_triggers.sql
docker compose start
```

详见 [`scripts/redistribute_triggers.sql`](./scripts/redistribute_triggers.sql) 注释。

---

## 7. 故障排查

详见 [`ubuntu.md`](./ubuntu.md) §10。常见几个：

| 现象 | 原因 + 解 |
|---|---|
| 容器启动后立即退出 | `docker compose logs` 看；多半 `.env` 没填或 `WANGUI_MASTER_KEY` 不是 64 字符 hex |
| 浏览器打开 502 | 1Panel 反代目标 `http://127.0.0.1:5555` 错或容器没起 |
| 朋友激活点「下一步」报「邀请码不存在」 | 邀请码拼错 / 已禁用 / 已被他人激活（不是他自己原激活码） |
| 朋友 step 2 粘链接报「学校 OAuth 登录失败」 | 回调链接已被用过（同一 code 只能换一次 token），让他重扫一次二维码 |
| 朋友 22:00 没签 | admin 「监控看板」搜他看分类 + 学校状态 chip：多半 token 过期 / 没绑宿舍楼 / 不在 signDays / 学校标了请假 |
| 测试邮件发不出 | Gmail 没开两步验证，或填了登录密码而不是 App Password |
| Server 酱测试报错 | SendKey 拼错 / 已过期（免费版每天 5 条上限） |
| 监控看板「已断开」红 chip | 反代没关闭 `proxy_buffering`；或浏览器在后台被节流，切回就重连 |
| 22:00 全员没签 | scheduler 容器时区不对：`docker compose exec wangui date` 看是不是 CST |
| 头像兜底显示首字 | 该用户激活时学校 CDN 抽风 fetch 失败；让他重新更新一次 token（自动重拉头像）|
| Token 提醒没发 | 看 logs 找 `token-warn sweep done`；每个 token 周期最多发一次，已发过 `token_warned_at` 非 0 不再重发 |

---

## 8. 安全模型

| 组件 | 措施 |
|---|---|
| 学校 token 落库 | AES-256-GCM 加密，密钥仅在 `.env` |
| SMTP 密码 | 同上加密 |
| Admin Server 酱 SendKey | 同上加密 |
| 用户 PIN | bcrypt cost 12 |
| Session | 64-byte 随机 ID，HttpOnly + SameSite=Lax，DB 存 expires_at |
| Admin 路径 | 混淆为 `/rosekhlifa`（不是 `/admin`），防扫描 |
| 邀请码 | 一码绑一学号；释放后可重激活 |
| 限流 | login / activate / precheck 共享 5/min IP 桶 |
| **两步激活** | step 1 通过 (precheck 邀请码) 才显示 step 2 的 OAuth UI；路人无邀请码看不到 OAuth 流程 |
| Guest 私密性 | 临时朋友无 PIN（不能从 wangui 登录），无邮箱（不收邮件），cleanup 后数据彻底删除 |
| Server 酱 SendKey 不回显 | 服务端只返回 `keySet: true/false`，前端不知道实际 key；改要重新输入 |
| Token 到期提醒去重 | `token_warned_at` 字段，每个 token 周期最多警告一次，UpdateToken 时重置 |
| SSE 鉴权 | EventSource 同 origin，复用 admin session cookie；4 小时自动断开强制重新鉴权 |
| tokengrab CA 私钥 | DPAPI 加密落地，仅本机本用户能解 |

---

## 9. 路线图（已交付）

- ✅ 微信 OAuth 主路径 + tokengrab 备用通道
- ✅ 两步式激活（隐藏 OAuth UI）
- ✅ 个人签到时刻 + jitter（22:00-22:10 内均匀分布）
- ✅ 周次掩码 / 自动签到 toggle
- ✅ 临时朋友 Guest 模式 + 自动 cleanup
- ✅ Token 自动刷新 modal（用户自助 + admin 代刷）
- ✅ Server 酱微信推送（用户 + admin 双通道）
- ✅ Token 剩 2 天自动提醒（邮件 + 微信）
- ✅ 学校规则每日 18:00 监控 + diff 通知
- ✅ 监控看板全员可见 + 6 类筛选 + SSE 实时事件流
- ✅ 用户卡片/列表双视图 + 批量操作 + 抽屉详情
- ✅ Dashboard 数据卡（连签 / 月度 / 总签到 / 历史最长）
- ✅ admin 日志 CSV 导出
- ✅ 学校 CheckinStatus 集成（请假 / 节假日 / 走读 可视化）

---

## 10. License

无 license。私有仓库，仅供仓库所有者和明确授权的个人使用。**禁止任何形式的转发、转载、二次分发**。

---

## 11. 维护者

- 仓库所有者：仅自己
- AI 协作者：开发期 90% 代码由 Claude（Anthropic）辅助生成 + 审阅 + 集成
- 用户：≤5 个真实信任的朋友（永久邀请码）+ 偶尔几个临时朋友（Guest 模式）

—

拿到代码 + 邀请码的人，意味着你被信任。**不要扩散**。
