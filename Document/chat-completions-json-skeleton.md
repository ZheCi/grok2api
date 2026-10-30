# Chat Completions JSON 骨架

`POST /v1/chat/completions` 的请求与返回字段说明。

约定：

- **必填**只有请求里的 `model` 和 `messages`。
- 可选字段没有就省略，不要填 `null`。
- JSON 本身不能写注释；下面用 `jsonc` 在字段旁加中文说明，方便对照，不能直接当请求体粘贴。
- Chat Completions **没有**加密思考字段。上游密文在 `/v1/responses` 的 `encrypted_content`，或 `/v1/messages` 的 `signature` / `redacted_thinking`。

---

## 1. 请求骨架

```jsonc
{
  "model": "grok-4.6",                          // 必填。对外模型名
  "messages": [                                 // 必填。对话历史，至少一条
    {
      "role": "system",                         // 角色：system / developer / user / assistant / tool
      "content": "系统提示，可省略"               // 文本内容；也可改成多模态数组
    },
    {
      "role": "user",
      "content": "用户问题"
    },
    {
      "role": "assistant",
      "content": "上一轮模型回复，多轮时才有",
      "tool_calls": [                           // 上一轮若在调工具才有
        {
          "id": "call_xxx",                     // 工具调用 ID，下一轮 tool 消息要回填
          "type": "function",                   // 固定为 function
          "function": {
            "name": "工具名",
            "arguments": "{\"key\":\"value\"}"  // 参数 JSON 字符串，不是对象
          }
        }
      ]
    },
    {
      "role": "tool",                           // 把工具结果交回模型
      "tool_call_id": "call_xxx",               // 对应上面的 id
      "name": "工具名",
      "content": "工具返回结果"
    }
  ],

  "stream": true,                               // 是否 SSE 流式；false 则一次性返回整包
  "stream_options": {
    "include_usage": true                       // 流式最后一帧是否带 usage
  },

  "temperature": 1.0,                           // 随机程度
  "top_p": 1.0,                                 // 核采样
  "max_tokens": 1024,                           // 最大输出 token（旧字段）
  "max_completion_tokens": 1024,                // 最大输出 token（优先于 max_tokens）
  "stop": ["STOP"],                             // 遇到这些字符串就停；也可写成单个字符串
  "user": "user-id",                            // 终端用户标识，会转成上游 safety_identifier
  "metadata": {},                               // 透传元数据
  "store": false,                               // 是否让上游保存这次 Response
  "service_tier": "auto",                       // 服务档位
  "parallel_tool_calls": true,                  // 是否允许并行工具调用
  "prompt_cache_key": "",                       // 提示缓存 / 会话粘性键

  "reasoning_effort": "xhigh",                  // 思考强度：auto / none / low / medium / high / xhigh

  "response_format": {                          // 约束输出格式
    "type": "json_schema",
    "json_schema": {
      "name": "result",                         // schema 名称
      "schema": {}                              // JSON Schema 本体
    }
  },

  "tools": [                                    // 可供模型调用的工具
    {
      "type": "function",
      "function": {
        "name": "工具名",
        "description": "说明",
        "parameters": {
          "type": "object",
          "properties": {},
          "required": []
        }
      }
    }
  ],
  "tool_choice": "auto",                        // auto / none / required，或指定某个函数
  "web_search_options": {}                      // 开启联网搜索时带上；网关可能补一条 web_search 工具
}
```

`content` 也可以不是字符串，而是多模态数组：

```jsonc
{
  "role": "user",
  "content": [
    { "type": "text", "text": "看看这张图" },     // 文本段
    {
      "type": "image_url",
      "image_url": { "url": "data:image/png;base64,..." }  // 图片 URL 或 data URI
    }
  ]
}
```

---

## 2. 非流式返回骨架

`stream: false` 时整包返回，`object` 为 `chat.completion`。

```jsonc
{
  "id": "chatcmpl_xxxxxxxx",                    // 本次补全 ID（上游 resp_ 会被改成 chatcmpl_）
  "object": "chat.completion",                  // 非流式固定为此值
  "created": 1789743306,                        // Unix 秒
  "model": "grok-4.6",                          // 实际模型名
  "choices": [
    {
      "index": 0,                               // 候选序号，当前网关一般只有 0
      "message": {
        "role": "assistant",                    // 固定 assistant
        "content": "给用户看的正文",              // 纯文本；若只有工具调用可为 null
        "reasoning_content": "思考过程",          // 明文思考；没有则省略。密文不会出现在 Chat JSON 里
        "refusal": "被安全策略拒绝时才有",         // 拒绝说明
        "annotations": [],                      // 引用 / 标注，没有则省略
        "tool_calls": [                         // 要调工具时才有
          {
            "id": "call_xxx",                   // 工具调用 ID
            "type": "function",
            "function": {
              "name": "工具名",
              "arguments": "{\"key\":\"value\"}" // 参数 JSON 字符串
            }
          }
        ]
      },
      "finish_reason": "stop"                   // stop / tool_calls / length / content_filter
    }
  ],
  "usage": {
    "prompt_tokens": 209,                       // 输入 token
    "completion_tokens": 23561,                 // 输出 token（含思考）
    "total_tokens": 23770,                      // 合计
    "prompt_tokens_details": {
      "cached_tokens": 128                      // 命中提示缓存的输入 token
    },
    "completion_tokens_details": {
      "reasoning_tokens": 20508                 // 思考占用的输出 token
    },
    "cost_in_usd_ticks": 1415920000,            // 费用，单位 1 USD = 10_000_000_000 ticks
    "num_sources_used": 0,                      // 服务端搜索用到的来源数
    "num_server_side_tools_used": 0,            // 服务端工具调用次数
    "context_details": {
      "input_tokens": 209,                      // 上下文口径的输入
      "output_tokens": 23561                    // 上下文口径的输出
    }
  }
}
```

`finish_reason`：

| 值 | 含义 |
|---|---|
| `stop` | 正常说完 |
| `tool_calls` | 要调工具，此时 `content` 可能是 `null` |
| `length` | 被长度截断 |
| `content_filter` | 被安全策略拒绝 |

---

## 3. 流式返回骨架

`stream: true` 时 HTTP 体是 SSE（`text/event-stream`）。  
每个 `data:` 后面是一块 JSON，`object` 为 `chat.completion.chunk`。  
变的是 `delta`，以及最后的 `finish_reason` / `usage`。

### 3.1 公共外壳

```jsonc
{
  "id": "fba611f7-5b36-9c1e-ba5e-1a1f644c5f15", // 同一轮对话保持不变
  "object": "chat.completion.chunk",            // 流式固定为此值
  "created": 1789743306,                        // Unix 秒
  "model": "grok-4.6",
  "choices": [
    {
      "index": 0,
      "delta": {},                              // 本帧增量，见下面几种形状
      "finish_reason": null                     // 未结束为 null；最后一帧才有值
    }
  ]
}
```

### 3.2 角色

```jsonc
{
  "id": "fba611f7-5b36-9c1e-ba5e-1a1f644c5f15",
  "object": "chat.completion.chunk",
  "created": 1789743306,
  "model": "grok-4.6",
  "choices": [
    {
      "index": 0,
      "delta": { "role": "assistant" },         // 先声明角色
      "finish_reason": null
    }
  ]
}
```

### 3.3 思考增量

```jsonc
{
  "id": "fba611f7-5b36-9c1e-ba5e-1a1f644c5f15",
  "object": "chat.completion.chunk",
  "created": 1789743306,
  "model": "grok-4.6",
  "choices": [
    {
      "index": 0,
      "delta": { "reasoning_content": "思考片段" }, // 明文思考碎片。密文不会进这个字段
      "finish_reason": null
    }
  ]
}
```

### 3.4 正文增量

把所有 `delta.content` 按顺序接起来，就是给用户看的答案。

```jsonc
{
  "id": "fba611f7-5b36-9c1e-ba5e-1a1f644c5f15",
  "object": "chat.completion.chunk",
  "created": 1789743306,
  "model": "grok-4.6",
  "choices": [
    {
      "index": 0,
      "delta": { "content": "正文片段" },        // 可能只有几个字符
      "finish_reason": null
    }
  ]
}
```

### 3.5 工具调用增量

首帧带齐 `id` / `type` / `name`：

```jsonc
{
  "id": "fba611f7-5b36-9c1e-ba5e-1a1f644c5f15",
  "object": "chat.completion.chunk",
  "created": 1789743306,
  "model": "grok-4.6",
  "choices": [
    {
      "index": 0,
      "delta": {
        "tool_calls": [
          {
            "index": 0,                         // 第几个工具调用
            "id": "call_xxx",
            "type": "function",
            "function": {
              "name": "工具名",
              "arguments": ""                   // 参数从空字符串开始往后拼
            }
          }
        ]
      },
      "finish_reason": null
    }
  ]
}
```

后续参数块通常只带：

```jsonc
{
  "delta": {
    "tool_calls": [
      {
        "index": 0,
        "function": { "arguments": "{\"key\":" } // 参数字符串碎片
      }
    ]
  }
}
```

### 3.6 结束 + 用量

`stream_options.include_usage: true` 时，最后一帧会带 `usage`。

```jsonc
{
  "id": "fba611f7-5b36-9c1e-ba5e-1a1f644c5f15",
  "object": "chat.completion.chunk",
  "created": 1789743306,
  "model": "grok-4.6",
  "choices": [
    {
      "index": 0,
      "delta": {},                              // 结束帧通常是空增量
      "finish_reason": "stop"                   // 与非流式相同的结束原因
    }
  ],
  "usage": {
    "prompt_tokens": 209,                       // 输入 token
    "completion_tokens": 23561,                 // 输出 token
    "total_tokens": 23770,                      // 合计
    "prompt_tokens_details": { "cached_tokens": 128 },
    "completion_tokens_details": { "reasoning_tokens": 20508 },
    "cost_in_usd_ticks": 1415920000,            // 费用 ticks
    "num_sources_used": 0,                      // 搜索来源数
    "num_server_side_tools_used": 0,            // 服务端工具次数
    "context_details": {
      "input_tokens": 209,
      "output_tokens": 23561
    }
  }
}
```

SSE 层再包一层：

```text
data: {上面那份 JSON}

data: [DONE]                                    // 流结束标记，不是 JSON 对象
```

网关还可能插入这两行注释（标准客户端会忽略，不是给 UI 渲染的）：

```text
: grok2api-reasoning-start                      // 思考开始
: grok2api-reasoning-evidence                   // 上游有加密思考证据，但密文本身不随 Chat JSON 下发
```

---

## 4. 把流式结果收成完整对象

客户端拼完后，逻辑上等价于非流式的 `chat.completion`：

```jsonc
{
  "id": "一次对话同一个 id",
  "object": "chat.completion",
  "created": 1789743306,
  "model": "grok-4.6",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "所有 delta.content 按顺序拼接",
        "reasoning_content": "所有 delta.reasoning_content 按顺序拼接",
        "tool_calls": []                        // 按 index 拼好的工具调用；没有则省略
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 0,
    "completion_tokens": 0,
    "total_tokens": 0,
    "prompt_tokens_details": { "cached_tokens": 0 },
    "completion_tokens_details": { "reasoning_tokens": 0 },
    "cost_in_usd_ticks": 0,
    "num_sources_used": 0,
    "num_server_side_tools_used": 0,
    "context_details": {
      "input_tokens": 0,
      "output_tokens": 0
    }
  }
}
```

对照：

| | 流式 chunk | 非流式整包 |
|---|---|---|
| `object` | `chat.completion.chunk` | `chat.completion` |
| 文本位置 | `choices[0].delta.content` | `choices[0].message.content` |
| 思考 | `delta.reasoning_content` | `message.reasoning_content` |
| 结束 | 某帧 `finish_reason` + `[DONE]` | 同一个对象里的 `finish_reason` |

---

## 5. 出错骨架

请求阶段失败（还没开始流）：

```jsonc
{
  "error": {
    "message": "错误说明",                       // 可读说明
    "type": "invalid_request_error",            // 错误大类
    "param": null,                              // 出错字段名，没有则为 null
    "code": "invalid_request"                   // 稳定错误码
  }
}
```

流已经开始后又失败，可能再推一块：

```jsonc
{
  "type": "error",                              // 流内错误事件
  "error": {
    "code": "upstream_stream_interrupted",      // 如 upstream_stream_idle_timeout / upstream_stream_incomplete
    "message": "上游流式响应中断"
  }
}
```

---

## 6. 日常对照

1. 请求：`model` + `messages` + 可选开关  
2. 非流式：`choices[0].message`  
3. 流式：很多个 `choices[0].delta`，最后 `[DONE]`

---

## 7. 加密思考在哪

Grok 上游的思考经常是密文，字段名是 **`encrypted_content`**。  
Chat Completions 兼容层**故意不把它放进公开 JSON**，避免密文泄漏到 `delta` / `message` 里。

三种协议对照：

| 协议 | 明文思考 | 加密思考 | 说明 |
|---|---|---|---|
| `/v1/chat/completions` | `reasoning_content` | **无此字段** | 有密文时最多插入 SSE 注释 `: grok2api-reasoning-evidence` |
| `/v1/responses` | `summary` / `content` 文本 | `output[].encrypted_content` | 要回放时请求 `include: ["reasoning.encrypted_content"]` |
| `/v1/messages` | `content[].thinking` | `signature`，或整块 `redacted_thinking.data` | Anthropic 形态；网关会把 signature 映射回 `encrypted_content` |

### 7.1 Chat：看不到密文

公开 chunk / 整包里只有明文：

```jsonc
{
  "delta": {
    "reasoning_content": "可读的思考"            // 仅明文
  }
}
```

若上游只有密文、没有明文，Chat JSON 里常常连 `reasoning_content` 都没有，只多两行 SSE 注释：

```text
: grok2api-reasoning-start
: grok2api-reasoning-evidence                   // 表示「有加密思考」，正文仍不给密文
```

质量守卫会扫描这些注释和上游 Responses 里的 `encrypted_content`，用来判断是不是假思考。短 stub 不算有效思考。

### 7.2 Responses：密文在这里

请求里声明要密文（Build 渠道往往会自动补上）：

```jsonc
{
  "model": "grok-4.6",
  "input": "用户问题",
  "reasoning": { "effort": "high" },            // 思考强度
  "include": ["reasoning.encrypted_content"]    // 向下游/上游要加密思考
}
```

返回的 `output` 里会有 reasoning 项：

```jsonc
{
  "id": "resp_xxx",
  "object": "response",
  "output": [
    {
      "id": "rs_1",                             // 思考项 ID
      "type": "reasoning",                      // 这是思考块，不是正文
      "summary": [                              // 可读摘要，可空
        { "type": "summary_text", "text": "思考摘要" }
      ],
      "encrypted_content": "gAAAA..."           // 加密思考密文，不透明，下一轮原样带回
    },
    {
      "type": "message",
      "role": "assistant",
      "content": [
        { "type": "output_text", "text": "给用户看的正文" }
      ]
    }
  ]
}
```

无状态多轮时，把上一轮的 reasoning 项原样放进下一轮 `input`，不要改 `encrypted_content`。

流式 Responses 里，密文通常出现在：

```jsonc
{
  "type": "response.output_item.done",          // 思考项完成
  "item": {
    "id": "rs_1",
    "type": "reasoning",
    "encrypted_content": "gAAAA..."             // 完成时才带齐密文
  }
}
```

### 7.3 Messages：密文改叫 signature

有明文思考时：

```jsonc
{
  "type": "thinking",
  "thinking": "可读思考",                       // 明文
  "signature": "gAAAA..."                       // 加密思考；对应 Responses 的 encrypted_content
}
```

只有密文、没有明文时：

```jsonc
{
  "type": "redacted_thinking",                  // 已脱敏的思考块
  "data": "gAAAA..."                            // 整段密文
}
```

下一轮把这两块原样放回 assistant 消息即可；网关会再映射成 Responses 的 `encrypted_content`。

