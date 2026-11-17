# STAR NOVEL CMS 批量新建书籍与分章导入 OpenAPI 文档（供 Codex 自动化接入）

本文档说明如何通过 CMS 管理端 OpenAPI（JWT 鉴权）一次性完成 **上传封面 + 单个全文 TXT 自动分章（或 ZIP） + 设置起始收费章节 + 保存为未上架草稿**，适用于运营使用 Codex 脚本批量录入新书，**无需前端页面操作**。

---

## 一、基础信息与鉴权

- **生产 Base URL**：`https://cms.starsfic.com/api/v1/admin`
- **认证方式**：`Authorization: Bearer <token>`
- **角色权限要求**：登录账号角色必须为 `SuperAdmin`、`Admin` 或 `Editor`（`MediaBuyer` / `Finance` / `Support` 返回 `403 Forbidden`）。

### 1.1 获取管理员 Token

- **接口**：`POST /api/v1/admin/auth/login`
- **Content-Type**：`application/json`
- **请求体**：
```json
{
  "username": "<admin_username>",
  "password": "<admin_password>"
}
```
- **成功响应 (`200 OK`)**：
```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "admin": {
    "id": "10000000-0000-0000-0000-000000000001",
    "username": "admin",
    "nickname": "超级管理员",
    "role": "SuperAdmin"
  }
}
```

> 后续所有请求均在 Header 中携带：
> `Authorization: Bearer <token>`

---

## 二、核心接口：批量新建书籍并自动分章导入

### `POST /api/v1/admin/novels/batch-import`

> **事务与原子性保证**：每本书在服务端独立按原子流程处理——先校验元数据、UTF-8 编码、正则分章结果，再上传封面 / 暂存章节存储，最后在同一数据库事务中写入 `novels` 与 `chapters` 并同步总字数。**单本书任意步骤失败（例如正则未匹配到章节、编码非 UTF-8、封面无效）不会留下空壳草稿书；同批次其余有效书籍正常创建并返回逐条明细。**

- **Content-Type**：`multipart/form-data`
- **单次请求体积上限**：`50 MB`（受前端 Nginx 网关上传限制，建议每次按 **1–5 本** 或 **总大小 ≤ 10 MB** 分批提交）
- **单次书籍上限**：`1` ~ `50` 本（`manifest` 数组元素数）

#### 2.1 表单字段说明（多本标准模式，推荐 Codex 使用）

| 表单字段名 | 类型 | 必填 | 说明 |
| :--- | :--- | :--- | :--- |
| `manifest` | JSON 字符串（对象数组或单对象） | **是** | 待新建书籍的配置清单，每个元素对应 1 本书（见下方字段定义） |
| `<cover_field>` | 二进制图片文件 | 视 `manifest` 而定 | 若某本书未传 `cover_url`，须提供 `cover_field` 并上传对应封面图片（仅支持 **JPG / PNG / WebP**，单张 ≤ 5 MB） |
| `<chapter_field>` | 二进制文本/压缩包文件 | **是** | 对应书籍的全文 `.txt`（UTF-8）或 `.zip` 文件（单文件 ≤ 50 MB） |

> 注：当仅导入 **1 本书** 且不方便构造 `manifest` JSON 时，也支持直接在 `multipart/form-data` 顶层传表单字段 `title`、`author`、`file`（章节文件）、`cover`（封面文件）或 `cover_url`、`start_pay_chapter_index`、`publish_status` 等简写字段。自动化脚本推荐始终使用 `manifest` 模式。

#### 2.2 `manifest` 单本书籍对象字段说明

| 字段名 | 类型 | 必填 | 默认值 | 说明 |
| :--- | :--- | :--- | :--- | :--- |
| `title` | `string` | **是** | — | 书名（非空 UTF-8，≤ 150 字符，不含控制符 `\x00`） |
| `author` | `string` | **是** | — | 作者名（非空 UTF-8，≤ 100 字符，不含控制符 `\x00`） |
| `chapter_field` | `string` | **是** | 仅单本时可省略并默认 `"file"` | 本次 `multipart/form-data` 中存放该书全文 TXT / ZIP 的文件字段名，例如 `"chapter_0"` |
| `cover_field` | `string` | 与 `cover_url` 互斥二选一 | `""` | 本次 `multipart/form-data` 中存放该书封面图片的文件字段名，例如 `"cover_0"`；服务端会自动上传至私有 OSS 并生成 `oss://covers/...`（注意：不可与 `cover_url` 同时填写） |
| `cover_url` | `string` | 与 `cover_field` 互斥二选一 | `""` | 通过 `POST /api/v1/admin/novels/cover-upload` 预先上传得到的内部引用（格式必须为 `oss://covers/...`，支持 `.jpg`/`.jpeg`/`.png`/`.webp`，**禁止传外部 `http(s)://` URL**） |
| `import_type` | `string` | 否 | `"single_txt"` | 分章模式：`single_txt`（单个全文 TXT 正则分章）或 `zip`（ZIP 内含多 TXT） |
| `delimiter_regex` | `string` | 否 | `(?i)(chapter\s+\d+\|第[一二三四五六七八九十百千万零两\d]+章)` | `single_txt` 模式下行首匹配的 Go 正则表达式；从行首起始匹配，匹配整行保留为章节名（超过 150 字自动截断），首个标题行前的序言/版权页不会入库 |
| `publish_status` | `string` | 否 | `"draft"` | 发布状态：`draft`（未上架草稿，**推荐保持默认或显式传 `"draft"`**）、`offline`、`published` |
| `status` | `string` | 否 | `"Ongoing"` | 连载状态：`Ongoing`（连载中）或 `Completed`（已完结） |
| `genres` | `string[]` 或逗号分隔字符串 | 否 | `["Contemporary", "Romance", "Cheating"]` | 分类标签数组；英文逗号拼接总长度不能超过 `2048` 字符 |
| `synopsis` | `string` | 否 | `""` | 书籍简介 |
| `rating` | `number` | 否 | `4.5` | 评分，范围 `0` ~ `5` |
| `start_pay_chapter_index` | `integer` | 否 | 继承全局起付章节配置（默认 `3`） | **1-based** 起始收费章节序号。例如传 `5` 时，第 `1~4` 章（内部 `chapter_index` `0~3`）免费，第 `5` 章及以后自动按字数计费 |
| `coin_cost_per_thousand` | `integer \| null` | 否 | `null`（继承全局千字单价） | 独立千字金币单价（`≥ 0` 整数）；传 `null` 或省略表示继承全局系统设置 |

#### 2.3 TXT 自动分章格式规范

使用 `import_type: "single_txt"` 时，需满足：
1. **文件编码**：必须为标准 **UTF-8**（带或不带 UTF-8 BOM `\ufeff` 均可，非 UTF-8 如 GBK 会被明确拒绝）。
2. **换行符**：`\r\n`、`\r`、`\n` 均自动标准化。
3. **标题行格式**：使用默认正则时，每章标题须**独立占一行且位于行首**（允许行首空格），例如：
   - `Chapter 1: The Encounter`
   - `chapter 2`
   - `第一章 初遇`
   - `第10章 真相`
4. **正文提取**：每个标题行到下一标题行之间的全部文本归入该章正文，空白序言不入库。若全篇未匹配到任何章节标题，该本书将导入失败且不会产生垃圾草稿记录。

#### 2.4 响应格式

- **HTTP `200 OK`**：批次中至少有 `1` 本创建成功（含部分成功、`failed_count > 0` 的情况，便于 Codex 根据 `results` 精准记录失败原因与重试）。
- **HTTP `400 Bad Request`**：请求参数错误、`manifest` 非法、或批次内所有书籍均创建失败（同样返回完整 `results` 明细）。
- **HTTP `401 Unauthorized` / `403 Forbidden`**：Token 缺失/过期或角色无权限。
- **HTTP `413 Request Entity Too Large`**：单次请求体超过 `50 MB`。

**响应示例 (`200 OK`)**：
```json
{
  "total": 2,
  "created_count": 1,
  "failed_count": 1,
  "results": [
    {
      "index": 0,
      "title": "The Alpha's Second Chance",
      "status": "created",
      "chapters_count": 85,
      "total_word_count": 168420,
      "novel": {
        "id": 10000042,
        "title": "The Alpha's Second Chance",
        "author": "Elena Vance",
        "cover_url": "https://oss.starsfic.com/covers/...",
        "cover_key": "oss://covers/2026/11/...",
        "rating": 4.5,
        "status": "Ongoing",
        "publish_status": "draft",
        "synopsis": "...",
        "genres": ["Contemporary", "Romance", "Cheating"],
        "word_count": 168420,
        "view_count": 0,
        "coin_cost_per_thousand": null,
        "effective_coin_cost_per_thousand": 500,
        "start_pay_chapter_index": 5,
        "created_at": "2026-11-17T07:20:00Z",
        "updated_at": "2026-11-17T07:20:00Z"
      }
    },
    {
      "index": 1,
      "title": "Broken Manuscript",
      "status": "failed",
      "error": "无效的章节文件: 未匹配到任何章节标题（例如 'Chapter 1' 或 '第一章'），请检查文件内容或分割正则"
    }
  ]
}
```

---

## 三、辅助接口说明

### 3.1 单独上传封面（可选两步法）

若 Codex 脚本希望对多本书复用同一封面或将大封面预先上传压缩请求体积：

- **接口**：`POST /api/v1/admin/novels/cover-upload`
- **Content-Type**：`multipart/form-data`，字段名 `file`（仅支持 JPG/PNG/WebP，≤ 5 MB）
- **响应 (`200 OK`)**：
```json
{
  "cover_key": "oss://covers/2026/11/a1b2c3....png",
  "preview_url": "https://oss.starsfic.com/covers/..."
}
```
随后在 `batch-import` 的 `manifest` 中传 `"cover_url": "<cover_key>"` 即可（不必再传 `cover_field`）。

### 3.2 审核完成后批量上架/下架

以 `"draft"` 导入后，运营人员或 Codex 脚本可在确认章节无误后调用批量上下架接口正式发布：

- **接口**：`PATCH /api/v1/admin/novels/batch-publish-status`
- **Content-Type**：`application/json`
- **请求体**：
```json
{
  "ids": [10000042, 10000043],
  "publish_status": "published"
}
```

---

## 四、Codex / 自动化脚本接入示例

### 4.1 cURL 示例（单次上传封面 + 全文 TXT 导入 1 本草稿）

```bash
TOKEN="<通过 /api/v1/admin/auth/login 获取的 JWT>"

curl -sS -X POST "https://cms.starsfic.com/api/v1/admin/novels/batch-import" \
  -H "Authorization: Bearer ${TOKEN}" \
  -F 'manifest=[{
    "title": "The Billionaire Secret",
    "author": "Mia Chen",
    "cover_field": "cover_0",
    "chapter_field": "chapter_0",
    "import_type": "single_txt",
    "status": "Ongoing",
    "publish_status": "draft",
    "genres": ["Contemporary", "Romance", "Cheating"],
    "synopsis": "Synopsis text here...",
    "rating": 4.6,
    "start_pay_chapter_index": 5
  }]' \
  -F "cover_0=@./books/book1/cover.jpg" \
  -F "chapter_0=@./books/book1/full.txt"
```

### 4.2 Python 批量导入模板（供 Codex 生成并执行）

下面的模板展示标准接入流程：**登录一次获取 Token → 自动按字节上限分批打包（每批 ≤ 8 MB 且 ≤ 5 本）→ 提交 `batch-import` → 输出成功 ID 与失败清单 JSON 报告**：

```python
import json
from pathlib import Path
import requests

BASE_URL = "https://cms.starsfic.com/api/v1/admin"
MAX_BATCH_BOOKS = 5
MAX_BATCH_BYTES = 8 * 1024 * 1024  # 单批控制 <= 8MB，避免触发网关体积上限

def login(username: str, password: str) -> str:
    resp = requests.post(
        f"{BASE_URL}/auth/login",
        json={"username": username, "password": password},
        timeout=20,
    )
    resp.raise_for_status()
    return resp.json()["token"]

def batch_import(token: str, books: list[dict]) -> list[dict]:
    """
    books 每项示例:
    {
      "title": "书名",
      "author": "作者",
      "cover_path": "/path/to/cover.jpg",
      "txt_path": "/path/to/full.txt",
      "start_pay_chapter_index": 5,
      "genres": ["Contemporary", "Romance", "Cheating"],
      "status": "Ongoing",
      "synopsis": "简介"
    }
    """
    headers = {"Authorization": f"Bearer {token}"}
    reports = []

    current_chunk: list[dict] = []
    current_bytes = 0

    def flush_chunk(chunk: list[dict]):
        if not chunk:
            return
        manifest = []
        files_to_close = []
        multipart = []
        try:
            for idx, item in enumerate(chunk):
                cover_field = f"cover_{idx}"
                chapter_field = f"chapter_{idx}"
                cf = open(item["cover_path"], "rb")
                tf = open(item["txt_path"], "rb")
                files_to_close.extend([cf, tf])
                multipart.append((cover_field, (Path(item["cover_path"]).name, cf)))
                multipart.append((chapter_field, (Path(item["txt_path"]).name, tf)))
                manifest.append({
                    "title": item["title"],
                    "author": item["author"],
                    "cover_field": cover_field,
                    "chapter_field": chapter_field,
                    "import_type": "single_txt",
                    "status": item.get("status", "Ongoing"),
                    "publish_status": "draft",
                    "genres": item.get("genres") or ["Contemporary", "Romance", "Cheating"],
                    "synopsis": item.get("synopsis", ""),
                    "rating": item.get("rating", 4.5),
                    "start_pay_chapter_index": item.get("start_pay_chapter_index", 3),
                })

            resp = requests.post(
                f"{BASE_URL}/novels/batch-import",
                headers=headers,
                data={"manifest": json.dumps(manifest, ensure_ascii=False)},
                files=multipart,
                timeout=180,
            )
            payload = resp.json()
            reports.extend(payload.get("results", [{"error": payload}]))
        finally:
            for fh in files_to_close:
                fh.close()

    for b in books:
        size = Path(b["cover_path"]).stat().st_size + Path(b["txt_path"]).stat().st_size
        if current_chunk and (len(current_chunk) >= MAX_BATCH_BOOKS or current_bytes + size > MAX_BATCH_BYTES):
            flush_chunk(current_chunk)
            current_chunk = []
            current_bytes = 0
        current_chunk.append(b)
        current_bytes += size

    flush_chunk(current_chunk)
    return reports
```
