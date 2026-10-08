# 勿外传 · 技术设计文档（TDD）

> 项目代号：wangui
> 用途：河南农业大学晚归签到的内部自动化工具，邀请制，≤ 5 人使用
> 起草日期：2026-05-14

---

## 0. 红线声明

**本项目仅供内部少数受邀朋友使用。绝不公开域名、绝不公开仓库、绝不接受陌生人。**

每位用户必须自己抓自己的学校 token，自行承担"实际不在校仍签到 = 谎报位置"的全部后果。运维方（部署者）只对工具本身的正确性负责。

---

## 1. 高层架构

```
┌────────────────────────────────────────────────────────────────┐
│                       浏览器 (用户/管理员)                       │
│                                                                │
│   ┌─────────────────┐                  ┌──────────────────┐   │
│   │ /login          │                  │ /rosekhlifa/login│   │
│   │ /  /settings    │   Vue 3 SPA      │ /rosekhlifa/*    │   │
│   │ /records /...   │  + vue-router    │  (admin pages)   │   │
│   └─────────────────┘                  └──────────────────┘   │
│              │                                  │             │
│              └───────────── fetch ──────────────┘             │
│                              │                                │
└──────────────────────────────┼────────────────────────────────┘
                               │ HTTPS (cookie session)
                               ▼
        ┌──────────────────────────────────────────────┐
        │            Go 单二进制 (wangui.exe)            │
        │                                              │
        │  ┌────────────────────────────────────────┐  │
        │  │ chi router (HTTP server :4444)         │  │
        │  │  ├─ /api/v1/login          (公开)      │  │
        │  │  ├─ /api/v1/activate       (公开)      │  │
        │  │  ├─ /api/v1/rosekhlifa/login (公开)    │  │
        │  │  ├─ /api/v1/me, /settings, /sign-now   │  │
        │  │  │      (用户 cookie 鉴权)              │  │
        │  │  ├─ /api/v1/rosekhlifa/*               │  │
        │  │  │      (管理员 cookie 鉴权)            │  │
        │  │  └─ /*  (embed.FS SPA 静态文件 + 字体)  │  │
        │  └────────────────────────────────────────┘  │
        │                                              │
        │  ┌────────────────────────────────────────┐  │
        │  │ 调度器 (scheduler/multi.go)             │  │
        │  │  每天 22:00 唤醒                        │  │
        │  │  → 取 auto_sign=1 的用户                │  │
        │  │  → 每个用户独立 goroutine               │  │
        │  │  → 22:00+trigger_min ± jitter 触发     │  │
        │  │  → 失败按 retry_count + retry_gap 重试 │  │
        │  │  → 22:30 关闭窗口                       │  │
        │  └────────────────────────────────────────┘  │
        │                                              │
        │  ┌────────────────────────────────────────┐  │
        │  │ 备份器 (backup/backup.go)               │  │
        │  │  每天 23:00 VACUUM INTO 快照            │  │
        │  │  保留 7 份，超出自动 GC                  │  │
        │  └────────────────────────────────────────┘  │
        │                                              │
        │  ┌────────────────────────────────────────┐  │
        │  │ 存储 (store, modernc.org/sqlite)        │  │
        │  │  data/wangui.db (单文件 SQLite WAL)     │  │
        │  │  data/master.key (AES-256 主密钥, 32B)  │  │
        │  │  data/backups/wangui-YYYYMMDD-*.db      │  │
        │  └────────────────────────────────────────┘  │
        └──────────────────────────────────────────────┘
                              │
                              │ HTTPS Bearer JWT
                              ▼
        ┌──────────────────────────────────────────────┐
        │ 河南农业大学晚归签到 API                       │
        │ https://xhbcs.henau.edu.cn/api/*             │
        │  - /auth/user, /auth/permissions             │
        │  - /checkin/available-rules                  │
        │  - /checkin/status?ruleId=                   │
        │  - /checkin (POST, 真正签到接口)              │
        └──────────────────────────────────────────────┘
```

**关键设计**：

- **零外部运行时依赖**：modernc.org/sqlite 纯 Go SQLite，不依赖 libc / cgo
- **前端打包进二进制**：Vite build → `cmd/wangui/web-dist/` → `go:embed` 编译进 exe
- **单文件部署**：~27MB exe + 一个 data 目录（sqlite + 主密钥）
- **学校 JWT 永不暴露浏览器**：浏览器只看到 web cookie（独立的随机 session id），学校 token AES-GCM 加密后存 sqlite

---

## 2. 项目目录

```
fuckwangui/
├── PLAN.md                    # 早期版本计划书（已被本文档取代）
├── final.md                   # 本文档
├── ubuntu.md                  # Ubuntu 部署教程
├── fuck.md                    # 第三方逆向参考
├── HarmonyOS_SansSC_Medium.ttf
├── JetBrainsMono-Regular.ttf
├── go.mod / go.sum
├── wangui.exe                 # 编译产物
├── data/
│   ├── wangui.db              # SQLite 主库 (gitignored)
│   ├── master.key             # AES 主密钥 (gitignored)
│   └── backups/               # 每日备份
│
├── cmd/wangui/
│   ├── main.go                # CLI 入口：doctor/status/sign/daemon/serve/backup-now
│   ├── embed.go               # //go:embed web-dist
│   └── web-dist/              # Vite build 产物 (gitignored 但 embed 时必需)
│
├── internal/
│   ├── api/                   # 学校 API 客户端（Bearer Token + REST）
│   │   ├── client.go          #   HTTP 封装 + 错误处理
│   │   ├── auth.go            #   /auth/user, /auth/permissions
│   │   └── checkin.go         #   /checkin/* 全套
│   │
│   ├── store/                 # SQLite 数据层
│   │   ├── store.go           #   连接 + migration
│   │   ├── crypto.go          #   AES-256-GCM token 加密
│   │   ├── users.go           #   users 表 CRUD
│   │   ├── codes.go           #   invite_codes 卡密
│   │   ├── dorms.go           #   dorm_locations 宿舍楼
│   │   ├── sessions.go        #   web_sessions web cookie
│   │   └── records.go         #   sign_records 签到流水
│   │
│   ├── scheduler/
│   │   ├── scheduler.go       #   Phase 1 单租户调度 (legacy)
│   │   └── multi.go           #   Phase 2 多租户调度 (生产用)
│   │
│   ├── web/                   # HTTP API
│   │   ├── server.go          #   chi router 装配
│   │   ├── handlers.go        #   用户端处理器
│   │   ├── admin_handlers.go  #   管理端处理器
│   │   ├── middleware.go      #   user/admin 鉴权 + cookie
│   │   ├── csrf.go            #   Origin/Referer 校验
│   │   ├── jwt.go             #   学校 JWT 解析
│   │   └── ratelimit.go       #   per-IP 限流
│   │
│   ├── backup/
│   │   └── backup.go          #   sqlite VACUUM INTO + GC
│   │
│   ├── notify/                #   预留通知渠道（目前只 log）
│   └── config/                #   Phase 1 YAML 配置（legacy）
│
└── web/                       # Vue 3 前端源码
    ├── package.json           # 依赖：vue, vue-router, leaflet, gcoord, lucide
    ├── vite.config.ts
    ├── tsconfig.json
    ├── index.html
    ├── public/fonts/          #   HarmonyOS + JetBrains Mono
    └── src/
        ├── main.ts            # 入口（先加载 theme，再 mount）
        ├── App.vue            # 根：水印背景 + RouterView + Toast
        ├── router.ts          # 路由配置（含登录态守卫）
        ├── style.css          # Tailwind v4 + @font-face + 主题变量
        ├── types.ts           # 全部 TS 类型定义
        ├── api.ts             # api / adminApi 两套客户端
        ├── lib/format.ts      # 时间/进度格式化
        ├── lib/toast.ts       # 简易 toast
        ├── stores/auth.ts     # useAuth / useAdminAuth (reactive)
        ├── stores/theme.ts    # 浅/深色 + localStorage
        ├── components/
        │   ├── UserLayout.vue, AdminLayout.vue   # 侧栏 + 主区
        │   ├── SidebarNav.vue, Logo.vue, Avatar.vue
        │   ├── ThemeToggle.vue
        │   └── MapPicker.vue  # Leaflet 地图选点 + 坐标系转换
        └── views/
            ├── Login.vue, Dashboard.vue, Settings.vue, Records.vue, Account.vue
            └── admin/
                └── Login.vue, Dashboard.vue, Codes.vue, Dorms.vue,
                    Users.vue, Logs.vue, Settings.vue
```

---

## 3. 数据模型 (SQLite)

```sql
-- 用户：学校 user_id (来自 JWT iss) 是主键
CREATE TABLE users (
  user_id              TEXT PRIMARY KEY,
  user_name            TEXT,
  user_number          TEXT,                   -- 学号
  user_section         TEXT,                   -- 学院
  user_class           TEXT,                   -- 班级
  user_avatar_url      TEXT NOT NULL DEFAULT '',
  token_enc            BLOB NOT NULL,          -- AES-GCM(学校 JWT)
  token_exp            INTEGER NOT NULL,       -- JWT exp (unix)
  auto_sign            INTEGER NOT NULL DEFAULT 1,
  is_disabled          INTEGER NOT NULL DEFAULT 0,
  invite_code          TEXT,                   -- 绑定的卡密
  pin_hash             BLOB,                   -- bcrypt(PIN)
  dorm_id              INTEGER,                -- 当前绑定的宿舍楼

  -- 调度配置（每用户可调）
  trigger_minute       INTEGER NOT NULL DEFAULT 2,
  jitter_sec           INTEGER NOT NULL DEFAULT 180,
  retry_count          INTEGER NOT NULL DEFAULT 3,
  retry_gap_min        INTEGER NOT NULL DEFAULT 5,

  -- 位置快照（从 dorm 复制过来，调度器直接读，无需 JOIN）
  lat                  REAL DEFAULT 0,
  lng                  REAL DEFAULT 0,
  address              TEXT DEFAULT '',
  city                 TEXT DEFAULT '',
  road                 TEXT DEFAULT '',
  poi                  TEXT DEFAULT '',
  send_address_fields  INTEGER NOT NULL DEFAULT 0,

  device_model         TEXT DEFAULT 'iPhone',
  device_system        TEXT DEFAULT 'iOS',
  saved_locations      TEXT NOT NULL DEFAULT '[]',   -- legacy, 未使用
  created_at           INTEGER NOT NULL,
  updated_at           INTEGER NOT NULL
);

-- 卡密：永久绑定到首次使用者
CREATE TABLE invite_codes (
  code           TEXT PRIMARY KEY,             -- XXX-XXX-XXXX
  bound_user_id  TEXT,                         -- null = 未使用
  bound_at       INTEGER,
  note           TEXT NOT NULL DEFAULT '',     -- 管理员备注
  disabled       INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  created_by     TEXT NOT NULL DEFAULT 'admin'
);

-- 宿舍楼：管理员预设，用户下拉选
CREATE TABLE dorm_locations (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  name                TEXT NOT NULL UNIQUE,
  latitude            REAL NOT NULL,           -- WGS84
  longitude           REAL NOT NULL,
  address             TEXT DEFAULT '',
  city                TEXT DEFAULT '',
  road                TEXT DEFAULT '',
  poi                 TEXT DEFAULT '',
  note                TEXT DEFAULT '',
  send_address_fields INTEGER NOT NULL DEFAULT 0,
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL
);

-- 签到流水：每次尝试一条
CREATE TABLE sign_records (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      TEXT NOT NULL,
  rule_id      INTEGER NOT NULL,
  status       TEXT NOT NULL,                  -- success / already / exempt / failed / skipped
  message      TEXT,
  occurred_at  INTEGER NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

-- Web 会话：用户 cookie 和管理员 cookie 共用一张表，is_admin 区分
CREATE TABLE web_sessions (
  session_id   TEXT PRIMARY KEY,               -- 32 字节随机 hex
  user_id      TEXT NOT NULL,                  -- 用户 ID 或 "__admin__"
  is_admin     INTEGER NOT NULL DEFAULT 0,
  expires_at   INTEGER NOT NULL
);
```

---

## 4. API 端点

所有 API 前缀 `/api/v1/`。所有 POST/PUT/DELETE 经过 CSRF Origin 校验 + 限流。

### 公开端点（无 cookie）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/login` | 学号 + PIN 登录（已激活用户） |
| POST | `/activate` | 卡密 + 学校 JWT + PIN（首次激活） |
| POST | `/rosekhlifa/login` | 管理员密码登录 |

### 用户端点（需 `wangui_session` cookie）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/me` | 当前用户全部信息（含 dormName） |
| PUT | `/token` | 更新学校 JWT |
| PUT | `/pin` | 改 PIN（旧 → 新） |
| GET | `/settings` | 配置 |
| PUT | `/settings` | 改配置（含 `dormId`） |
| GET | `/records` | 签到历史 |
| GET | `/dorms` | 宿舍楼列表（让用户下拉选） |
| POST | `/sign-now` | 立即触发一次签到尝试 |
| POST | `/logout` | 删自己的 session |
| DELETE | `/me` | 注销账号（删 user + 释放卡密） |

### 管理员端点（需 `wangui_admin` cookie）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/rosekhlifa/me` | 检查 admin 登录态 |
| POST | `/rosekhlifa/logout` | 退出 |
| GET | `/rosekhlifa/stats` | 概览数据 |
| GET | `/rosekhlifa/codes` | 卡密列表（含 `boundUserName`） |
| POST | `/rosekhlifa/codes` | 批量生成卡密 |
| PUT | `/rosekhlifa/codes/{code}` | 改备注 / 启用禁用 |
| DELETE | `/rosekhlifa/codes/{code}` | 删除（仅未用） |
| GET | `/rosekhlifa/users` | 用户列表（含 `dormName`） |
| GET | `/rosekhlifa/users/{id}` | 单用户详情 + 最近签到 |
| PUT | `/rosekhlifa/users/{id}` | 禁用 / 启用 / 改 autoSign |
| POST | `/rosekhlifa/users/{id}/pin` | 重置用户 PIN（可生成 6 位随机） |
| DELETE | `/rosekhlifa/users/{id}` | 删用户 + 释放卡密 |
| GET | `/rosekhlifa/dorms` | 宿舍楼列表（含 `users` count） |
| POST | `/rosekhlifa/dorms` | 添加宿舍楼 |
| PUT | `/rosekhlifa/dorms/{id}` | 修改 |
| DELETE | `/rosekhlifa/dorms/{id}` | 删除（仅无用户绑定） |
| GET | `/rosekhlifa/dorms/{id}/users` | 该宿舍楼的绑定用户列表 |
| GET | `/rosekhlifa/logs` | 全局签到流水（含 userName） |

---

## 5. 关键流程

### 5.1 用户激活（首次）

```
1. 朋友访问 https://your.domain/
2. 自动跳 /login，默认显示「登录」tab
3. 点「首次使用？前往激活」→ 切到「激活」tab
4. 输入卡密 + 学校 JWT + 设置 PIN (4-6 位数字, 两遍一致) + 勾选免责
5. POST /api/v1/activate
   ├─ 限流检查 (5/min/IP)
   ├─ CSRF Origin 校验
   ├─ 解 JWT 拿 iss + exp（不验证签名，只读 claim）
   ├─ 调学校 /auth/user 验证 token 真实有效
   ├─ Guard: 此 user_id 是否已绑过其它卡密？是 → 403
   ├─ BindCode (原子事务): 卡密未绑 → 绑给此 user_id；已绑同 user_id → 幂等通过
   ├─ bcrypt(PIN) → pin_hash
   ├─ AES-GCM(JWT) → token_enc
   ├─ UPSERT users 表
   └─ 颁发 wangui_session cookie (30 天)
6. SPA 接收 cookie → 跳 /
```

### 5.2 后续登录

```
1. 朋友访问 / → 跳 /login (默认在「登录」tab)
2. 输入学号 + PIN
3. POST /api/v1/login
   ├─ 限流检查
   ├─ 即便 user 不存在也跑一遍假 bcrypt（防时序枚举）
   ├─ FindByNumber → 拿 user
   ├─ bcrypt.Compare(PIN_hash, 输入)
   ├─ 检查 is_disabled
   └─ 颁发 wangui_session cookie
4. SPA 接收 cookie → 跳 /
```

### 5.3 用户配置宿舍楼

```
1. 用户进 /settings → 「我的宿舍楼」section
2. 下拉列表来自 GET /api/v1/dorms（管理员维护）
3. 选中一个 → PUT /api/v1/settings { dormId: N }
4. 后端：
   ├─ GetDorm(N)
   ├─ 把 dorm 的 lat/lng/address/city/road/poi/send_address_fields 快照到 user 表
   ├─ 设 user.dorm_id = N
   └─ 返回新 settings
5. 之后调度器直接读 user 表的快照（无需 JOIN dorm）
   ├─ 即使管理员后续改了 dorm，已绑用户**不**自动更新
   └─ 用户重新选一次同一个 dorm 即可触发新快照
```

### 5.4 每天自动签到

```
00:00 - 21:59  调度器睡眠（time.After 到下一个 22:00）
22:00:00       调度器醒来
               ├─ ListAutoSignUsers (auto_sign=1 AND is_disabled=0)
               ├─ 对每个用户 spawn goroutine
               └─ runForUser(uid, deadline=22:30):
                    ├─ jitter = rand.IntN(jitter_sec + 1)
                    ├─ sleep until 22:00 + trigger_minute + jitter
                    ├─ for attempt in 1..(1 + retry_count):
                    │    ├─ GetUser (re-read, 用户可能中途改 auto_sign)
                    │    ├─ SignOnce(user):
                    │    │    ├─ GET /checkin/status?ruleId=1
                    │    │    ├─ canCheckin=false → SignResult.failed
                    │    │    ├─ isBoarding=true → exempt
                    │    │    ├─ isExempt=true → exempt
                    │    │    ├─ hasCheckedIn=true → already
                    │    │    └─ POST /checkin {ruleId, lat, lng, deviceModel,
                    │    │                       deviceSystem, [address fields if set]}
                    │    ├─ INSERT sign_records
                    │    ├─ if Terminal (success/already/exempt) → break
                    │    └─ sleep retry_gap_min minutes
                    └─ 22:30 deadline 触发 ctx.cancel → 退出
22:30 - 23:59  下一轮 sleep
23:00          备份 goroutine 跑 VACUUM INTO + GC（独立于调度器）
```

### 5.5 签到请求体

```json
// 仅坐标模式 (默认, dorm.send_address_fields=false)
{
  "ruleId": 1,
  "latitude": 34.00000,
  "longitude": 113.00000,
  "deviceModel": "iPhone",
  "deviceSystem": "iOS"
}

// 含地址模式 (dorm.send_address_fields=true)
{
  "ruleId": 1,
  "latitude": 34.00000,
  "longitude": 113.00000,
  "deviceModel": "iPhone",
  "deviceSystem": "iOS",
  "locationAddress": "...",
  "city": "...",
  "road": "...",
  "poi": "..."
}
```

`omitempty` 让空字符串字段不出现在 JSON 中。

---

## 6. 安全机制

| 风险 | 缓解 |
|---|---|
| **Token 数据库泄露** | AES-256-GCM 加密，主密钥从 `WANGUI_MASTER_KEY` 环境变量读取（生产环境必设），不写任何文件 |
| **PIN 数据库泄露** | bcrypt(cost=10) 哈希，配合 IP 限流，4-6 位数字 PIN 暴力破解需 ~16000+ 小时 |
| **学校 JWT 暴露给浏览器** | 永远不会。浏览器只见 `wangui_session` cookie（32 字节随机 hex） |
| **CSRF** | 所有 mutating endpoint Origin/Referer 校验；SameSite=Lax cookie |
| **暴力破解登录** | IP 限流 5 次/分钟（in-memory，重启清零） |
| **用户枚举** | "学号或 PIN 不正确" 统一报错；不存在的 user 也跑假 bcrypt（时序防御） |
| **会话劫持** | HttpOnly cookie；生产环境 HTTPS Only |
| **Admin 路径扫描** | `/admin/*` 已改成 `/rosekhlifa/*`，前后端均改 |
| **同一 user_id 绑多张卡密** | activate 时 Guard：已有卡密的 user_id 拒绝新激活 |
| **空坐标签到** | 调度器 + 用户端均 check `lat == 0` → 标记 failed |
| **学校 API 拒签** | 调度器记录失败原因，retry 3 次。token 401 → 标记 "token 已失效" |

---

## 7. 命令行

```
wangui doctor           # Phase 1 自检（依赖 config.yaml，legacy）
wangui status           # Phase 1 查询签到状态
wangui sign             # Phase 1 立即手动签
wangui daemon           # Phase 1 单租户后台
wangui serve            # ★ Phase 2 生产入口
wangui backup-now       # 立即触发一次备份
wangui help

flags for serve:
  -addr 127.0.0.1:4444  # 默认监听
  -data ./data          # SQLite + master.key 目录

env vars:
  WANGUI_ADMIN_PASS         # 管理员密码（必设；不设管理面板禁用）
  WANGUI_MASTER_KEY         # AES 主密钥 hex(32 byte)；不设则自动生成到 data/master.key（仅开发）
  WANGUI_TRUSTED_ORIGINS    # CSRF 白名单（逗号分隔，例 http://127.0.0.1:5173 用于本地 vite dev）
```

---

## 8. 开发模式

```bash
# 终端 1：起后端（生产路径）
WANGUI_ADMIN_PASS=<生成的随机强密码> ./wangui.exe serve

# 终端 2：起 Vite dev server（前端热更）
cd web
npm run dev   # http://127.0.0.1:5173 (proxy /api 到 :4444)
```

修改前端文件 → 浏览器即时刷新。改 API 后构建：

```bash
cd web && npm run build   # 写到 cmd/wangui/web-dist/
cd ..  && go build -o wangui.exe ./cmd/wangui
```

---

## 9. 默认值速查

| 项 | 值 |
|---|---|
| **监听端口** | `127.0.0.1:4444` |
| **管理员密码** | 仅从未提交的 `.env` 读取随机强密码 |
| **管理员路径** | `/rosekhlifa/login` |
| **学校签到窗口** | 22:00 – 22:30 |
| **新用户默认触发** | 22:02 + 0~180s jitter |
| **默认重试** | 3 次，每 5 分钟一次 |
| **Cookie 有效期** | 用户 30 天 / 管理员 7 天 |
| **签到限流** | 5 次/IP/分钟 |
| **默认中心** | 不提供；由管理员在服务器私有配置中填写 |

---

## 10. 邮件通知 (SMTP)

**实现位置**：`internal/notify/email.go` (smtp 客户端) + `internal/notify/dispatch.go` (业务封装)

**配置方式**：管理员后台 `/rosekhlifa/settings` → SMTP 表单
- Host / Port (默认 smtp.gmail.com:587, STARTTLS)
- 发件人邮箱 (SMTP Username)
- 应用专用密码 (Gmail App Password, 16 位) → **AES-GCM 加密后存 SQLite**
- From 显示名 (可选)
- 管理员收件邮箱 (全局 Bcc)
- 总开关 (enabled)

**触发时机**（只在调度器自动签到，**不在 sign-now**）：
- 用户拿到终态 (success / already / exempt) 时
- 用户最后一次重试失败时
- 每用户每天每种结果**至多 1 封**

**收件规则**：

| 用户开通知 | 管理员 BCC 配了 | 行为 |
|---|---|---|
| ✓ | ✓ | 发给用户，bcc 管理员 |
| ✓ | ✗ | 只发给用户 |
| ✗ | ✓ | **以管理员日志的形式**发给管理员（subject 加 `[管理员日志]` 前缀） |
| ✗ | ✗ | 不发 |

**API 端点**：
- `GET /api/v1/rosekhlifa/smtp` — 读配置（密码字段返 `passwordSet: bool`，不回密码本体）
- `PUT /api/v1/rosekhlifa/smtp` — 改配置（password 空字符串 = 保留旧值）
- `POST /api/v1/rosekhlifa/smtp/test` — 发测试邮件给 adminBcc / 自己

**用户端**：`/settings` → 「邮件通知」section → toggle + email input

---

## 11. 已知限制 / 未做

- ❌ 多坐标快速切换：schema 留了 `saved_locations` 但前端没暴露
- ❌ 单元测试：所有逻辑靠端到端 curl + 手测
- ❌ 学校窗口/规则可能变化：写死了 ruleId=1 + 22:00-22:30，未来需要兼容
- ❌ IP 限流是内存版：重启清零；不适合多实例部署（当前不支持多实例）
- ❌ 邮件去重：当前每用户每个窗口最多 1 封（调度器逻辑保证）；如果你 sign-now 时也想要邮件，需要改 scheduler.SignOnce → dispatcher.DispatchSignResult 调用位置

---

## 12. 部署

见 `ubuntu.md`。
