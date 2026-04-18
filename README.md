# Save Token Gateway

一个面向 OpenAI 兼容接口的本地节流与节省网关。  
它的目标不是替代上游模型服务，而是在你的应用和上游 API 之间增加一层可本地运行的代理，把常见的 token 浪费场景尽量拦下来。

## 项目作用

这个程序主要解决三类问题：

1. 重复问题重复计费  
   通过精确缓存和语义缓存，尽量复用已经得到过的回答。

2. 对话历史越来越长  
   通过滑窗裁剪、输入瘦身和结构化预处理，减少不必要的上下文体积。

3. 接入多个客户端时改造成本高  
   对下游保持 OpenAI 兼容接口，应用通常只需要把 `base_url` 改到本地网关即可。

## 当前能力

- 兼容 `POST /v1/chat/completions`
- 兼容 `POST /chat/completions`
- 兼容 `POST /v1/embeddings`
- 兼容 `POST /embeddings`
- 兼容 `GET /v1/models`
- 兼容 `GET /models`
- 支持非流式和流式透传
- 支持 OpenAI 兼容上游转发
- 支持精确缓存
- 支持基于本地 embedding 的语义缓存
- 支持输入瘦身与滑窗裁剪
- 支持本地配置页面
- 支持本地“保守 / 平衡 / 激进”三档策略

## 技术栈

- 后端：Go
- 存储：SQLite
- 本地语义缓存 embedding：Ollama 兼容接口
- 前端：内嵌静态页面，由 Go 服务直接提供

## 目录说明

- [`cmd/gateway`](./cmd/gateway)：程序入口
- [`internal/handler`](./internal/handler)：HTTP 接口与本地页面
- [`internal/intelligent`](./internal/intelligent)：压缩、缓存、语义提取、预处理
- [`internal/localapp`](./internal/localapp)：本地模式配置与概览
- [`config.json.example`](./config.json.example)：通用服务模式示例配置
- [`config.local.example.json`](./config.local.example.json)：本地单机模式示例配置

## 快速开始

### 方式一：本地单机模式

适合你现在这种“我只想本地跑一个节省 token 的代理程序”的用法。

1. 复制本地示例配置

```powershell
Copy-Item .\config.local.example.json .\config.json
```

2. 启动服务

```powershell
go run .\cmd\gateway
```

3. 打开本地页面

```text
http://127.0.0.1:8080
```

4. 在页面里填写

- 上游 `Base URL`
- 上游 `API Key`
- 节省策略

5. 让你的客户端改连本地网关

- 本地 `Base URL`：`http://127.0.0.1:8080`
- 本地 `API Key`：任意非空字符串即可

说明：
本地 `API Key` 只用于兼容下游 SDK 的必填校验，真正转发时会使用你在本地页面里保存的上游密钥。

### 方式二：作为通用网关服务运行

适合需要更完整配置、路由和管理能力的场景。

1. 复制配置文件

```powershell
Copy-Item .\config.json.example .\config.json
```

2. 按需编辑 `config.json`

3. 启动服务

```powershell
go run .\cmd\gateway
```

## Docker 运行

```powershell
docker build -t save-token-gateway .
docker run --rm -p 8080:8080 -v ${PWD}/config.json:/app/config.json save-token-gateway
```

## 配置说明

### 本地单机模式

[`config.local.example.json`](./config.local.example.json) 中最重要的是：

- `local.enabled`
- `local.default_base_url`
- `local.default_strategy`
- `cache.semantic_embedding`

### 语义缓存

默认语义缓存使用 Ollama 兼容 embedding 接口，例如：

```json
{
  "cache": {
    "semantic_enabled": true,
    "semantic_embedding": {
      "provider": "ollama",
      "base_url": "http://127.0.0.1:11434",
      "model": "nomic-embed-text"
    }
  }
}
```

确保本机可访问对应模型，否则语义缓存会退化为未命中状态。

## 接口示例

### Chat Completions

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer local-not-used" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      {"role": "user", "content": "请只回复 你好"}
    ]
  }'
```

### Embeddings

```bash
curl http://127.0.0.1:8080/v1/embeddings \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer local-not-used" \
  -d '{
    "model": "text-embedding-3-small",
    "input": "hello world"
  }'
```

## 节省策略

### Conservative

- 保留稳定前缀
- 开启精确缓存
- 尽量不改写请求内容

### Balanced

- 包含 Conservative 全部能力
- 开启语义缓存
- 开启基础输入瘦身
- 开启滑窗裁剪

### Aggressive

- 包含 Balanced 全部能力
- 更强的输入压缩
- 更严格的输出约束

## 开发与测试

```powershell
go test ./...
```

如需重新编译：

```powershell
go build -o save-token.exe .\cmd\gateway
```

## 开源说明

仓库不应提交以下内容：

- 真实 `config.json`
- 数据库文件
- 日志文件
- 可执行文件
- 任何真实 API Key、Cookie、OAuth Secret、SMTP 密码

当前仓库已通过 `.gitignore` 排除这些运行时文件，但在发布前仍建议再做一次人工检查。

## 已知建议

- 如果你准备给第三方使用，优先使用通用 OpenAI 兼容上游示例，不要把私人中转站地址作为默认配置
- 如果你在多客户端环境中接入，建议先用 `Balanced`
- 如果你依赖工具调用或结构化会话，上游预处理必须保留消息附加字段，避免破坏 `tool_calls`

## License

默认准备使用 MIT License。  
如果你希望换成 Apache-2.0、GPL 或其他许可证，可以在发布前调整。
