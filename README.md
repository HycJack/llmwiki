# LLM Wiki — 本地知识库问答桌面应用

一个**单可执行文件**的桌面应用（基于 [MyGo](https://github.com/egoist/mygo)，GPU
原生渲染、无 WebView）。它把源文档导入成本地知识库（自动切片入库），并在不依赖
RAGFlow 服务器、数据库、MinIO、检索索引或 NATS 的前提下，用可选配置的
OpenAI 兼容接口做问答。离线时可退回**纯本地检索证据**。

---

## 功能

- **知识库管理**：创建 / 打开 / **重命名** / 删除知识库；每个知识库 = 数据根下一个目录。
  采用 onevra 式导航：**左侧边栏**是「知识库 / 问答」菜单（可收起），底部固定「配置」入口；
  「知识库」页先展示**知识库卡片网格**（名称、文档/切片数、更新时间），点某张卡片进入该库的
  **文件列表详情页**（内有「← 知识库」返回）；打开库默认不折叠/不预选文档，列表完整可见。
- **文档导入**：原生文件对话框多选 `.txt/.md/.html` → 自动解析、切片、向量化，写入
  三层 store（document → chunk → wiki 占位）并刷新计数。也可在知识库页**粘贴网页 URL 一键导入**：
  应用抓取网页正文，按 Content-Type 选解析器（html/markdown/文本）走同一套切片入库流程。
- **切片可视化**：文档表显示每篇的**切片数 / 字符数**；顶部有**切片分布条**（每篇切片数占比，
  悬停看名字与片数，最长的颜色最深）；点选一篇文档可在下方**逐片浏览**（序号 / 字符数 / 正文预览）。
- **接口配置（弹窗）**：配置 OpenAI 兼容接口（服务地址/Key/对话模型/向量模型），带「测试连接」，
  可选 mock 离线模式；配置作为**居中弹窗**打开，不再占用独立页面。
- **问答（多库）**：在问答页可**多选知识库**，对选中的库做本地混合检索（关键词 + 向量），
  命中证据跨库合并、去重后拼进提示词，交给配置的模型生成回答并附带引用出处；
  **答案用 Markdown 渲染**（标题 / 列表 / 引用 / 代码块 / 行内加粗·斜体·`代码`·链接），不再是纯文本；
  未配置模型时返回纯检索证据。

产品只有一个可执行文件 `bin/llmwiki-app.exe`。数据根默认自动选择**第一个可写的位置**
（`用户配置目录/LLMWiki` → `文档/LLMWiki` → `用户主目录/LLMWiki` → 可执行文件旁的 `llmwiki-data`，
按顺序取最先可创建的）；每次启动都会记住上次用到的数据根（`%LOCALAPPDATA%/LLMWiki/last-root.txt`），
所以无论从哪个目录重新打开，配置与知识库都落在**同一个绝对目录**，不会丢。也可用 `-root` 显式覆盖。

---

## 构建与运行

```bash
cd standalone/llmwiki
go build -o bin/llmwiki-app.exe ./cmd/llmwiki-app

./bin/llmwiki-app.exe            # 打开窗口
./bin/llmwiki-app.exe --version  # 版本（无窗自检）
./bin/llmwiki-app.exe --help     # 用法
./bin/llmwiki-app.exe -root D:/data  # 指定数据根目录
```

> 需要本机图形环境；窗口由 GPU 原生绘制。适配置的 OpenAI 兼容接口写在**侧边栏底部的
> 「配置」弹窗**里（含 /v1 的服务地址 + Key + 模型）。

---

## 架构

```
bin/llmwiki-app.exe  (单一可执行文件)
│
├─ cmd/llmwiki-app       产品入口：-root/--version/--help，创建 gui.App
├─ internal/gui          MyGo 界面：侧边栏（知识库）+ 问答视图，配置/新建/删除用弹窗
　                       + 原生文件对话框
└─ internal/kbservice   领域层（不依赖 GUI）：
    ├─ kb.go       知识库 建/列/开/删，每库一个目录 + meta.json
    ├─ importer.go 导入：input 解析切片 → embedding → 写入 store 三层
    ├─ qa.go       检索命中 +（可选）LLM 生成回答与引用
    └─ config.go   OpenAI 兼容接口配置（config.json）
                        │
                        └─ 复用既有内部底座
                           internal/input   解析与切片
                           internal/store   三层落库 + 混合检索（Index/Audit）
                           internal/engine  OpenAI 兼容 ChatInvoker/Embedder
                           internal/wiki    知识编译管线（底层复用）
```

### 复用底座（来自独立摘出的 llmwiki 内核）

前身是 RAGFlow LLM-Wiki 的独立移植。领域与界面之上仍复用这些内部包：

| 包 | 说明 |
|---|---|
| `internal/kccommon/` | 依赖轻量的公共类型 / `Deps` 接口 / 内存存储 |
| `internal/wiki/` | wiki 变体 MAP/REDUCE/PLAN/REFINE 管线（`wiki.Run`） |
| `internal/structure/` | `MergeIntoStore` / `CosineDecider` / `MergeStats` |
| `internal/runtime/` | `ReportProgressMessage` 垫片 |
| `internal/common/` | 日志 + `IsTransientError` / `StripThinkTrailing` 垫片 |
| `internal/tokenizer/` | 轻量 token 估算垫片 |
| `internal/engine/` | OpenAI 兼容 `ChatInvoker`/`Embedder` + 内存 WikiPageStore |
| `internal/input/` | txt/md/html 解析分块 |
| `internal/store/` | 三层落库（document/chunk/wiki）+ 无信息丢失审计 + 本地混合检索 |
| `internal/gui/` | MyGo 图形界面（知识库/配置/问答视图 + 原生文件对话框） |
| `internal/kbservice/` | 产品领域层（建库/导入/问答/配置） |

---

## 数据形态

```
<数据根>/               （默认 用户配置目录/LLMWiki）
├── config.json          OpenAI 兼容接口配置（明文保存在本机）
└── <知识库名>/          每库一个目录
    ├── meta.json        库的元数据（名称/时间/文档数/切片数）
    └── store/
        ├── documents.jsonl   layer1 文档清单
        ├── blobs/<docID>.bin layer1 原始字节（保真）
        ├── chunks.jsonl      layer2 切片（text + vector）
        └── wiki.jsonl        layer3 wiki 产品（本期为空，预留知识编译）
```

导入与问答共享这套 `internal/store`：导入写层1/2，问答用 `store.Index` 做
关键词+向量混合检索。删除知识库时整目录移除（有二次确认）。

---

## 测试

```bash
cd standalone/llmwiki
go test ./...
```

- `internal/kbservice/`：建库/导入落库/中文与英文检索命中/生成式问答/配置读写。
- `internal/gui/`：无窗自检 —— App 初始化、配置加载、经 GUI 走导入→问答的领域闭环。
- `internal/store/`、`internal/input/`、`internal/wiki/`：内核回归。

> GUI 窗口本身需真实显示环境；沙箱/CI 用 `--version` 与上面无窗测试覆盖启动与领域逻辑。