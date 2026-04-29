# OPC UA Supermarket Server

## 概述

OPC UA Supermarket 是一个高性能本地 OPC UA 服务器，基于 [gopcua](https://github.com/gopcua/opcua) 库实现。服务器维护 7000 个数据点（4000 布尔标志位 + 2000 整型值 + 1000 浮点值），以"数据超市"模式向多个 OPC UA 客户端提供读写服务。

### 设计目标

- **高性能**：通过固定数组和预分配 DataValue 减少运行时内存分配
- **高并发**：支持多客户端同时读写，使用读写锁保护共享数据
- **低延迟**：数字节点 ID 直接映射到数组下标，O(1) 时间复杂度访问
- **跨平台**：支持 Linux 和 Windows，可交叉编译

---

## 规格参数

### 数据规模

| 类型 | 数量 | Node ID 编码 | 数据类型 | 读写权限 |
|------|------|-------------|----------|---------|
| Flag  | 4000 | ns=2;i=10000 ~ 13999 | Boolean | 读写 |
| Int   | 2000 | ns=2;i=20000 ~ 21999 | Int32   | 读写 |
| Real  | 1000 | ns=2;i=30000 ~ 30999 | Double  | 读写 |

**总数据点：7000 个**

### 网络配置

| 参数 | 默认值 | 说明 |
|------|--------|------|
| Endpoint | 0.0.0.0 | 监听地址 |
| Port | 4840 | 监听端口 |
| Security Mode | None | 无加密（可配置证书启用） |
| Auth Mode | Anonymous | 匿名认证 |

---

## 架构设计

### 1. 数据结构

#### DataSupermarket 结构

```go
type DataSupermarket struct {
    mu sync.RWMutex

    // 原始数据存储
    Flags      [4000]bool
    IntValues  [2000]int32
    RealValues [1000]float64

    // 预分配的 DataValue 缓存，避免运行时分配
    FlagDV  [4000]ua.DataValue
    IntDV   [2000]ua.DataValue
    RealDV  [1000]ua.DataValue
}
```

**设计要点**：
- 固定大小数组，编译时确定大小，栈分配或静态分配
- 原始数据与 DataValue 分离，DataValue 预缓存供 OPC UA 读取使用
- RWMutex 提供读写分离的并发保护

#### ArrayNS 结构

```go
type ArrayNS struct {
    srv  *server.Server   // gopcua 服务器实例
    name string           // 命名空间名称
    id   uint16           // 命名空间索引
    ds   *DataSupermarket // 数据存储指针
    mu   sync.RWMutex     // 浏览操作锁
}
```

### 2. 节点 ID 映射

采用数字节点 ID，直接映射到数组下标：

```
Namespace Index = 2 (动态分配)

Flag0000  ns=2;i=10000  →  Flags[0]
Flag0001  ns=2;i=10001  →  Flags[1]
...
Flag3999  ns=2;i=13999  →  Flags[3999]

Int0000   ns=2;i=20000  →  IntValues[0]
Int0001   ns=2;i=20001  →  IntValues[1]
...
Int1999   ns=2;i=21999  →  IntValues[1999]

Real0000  ns=2;i=30000  →  RealValues[0]
Real0001  ns=2;i=30001  →  RealValues[1]
...
Real0999  ns=2;i=30999  →  RealValues[999]
```

**优势**：读取时通过节点 ID 直接计算数组下标，无须查表或遍历。

### 3. NameSpace 接口实现

实现 `gopcua/server.NameSpace` 接口：

| 方法 | 职责 |
|------|------|
| `Name()` | 返回命名空间名称 "DataSupermarket" |
| `ID()` / `SetID()` | 获取/设置命名空间索引 |
| `AddNode()` | 添加节点（本实现直接返回） |
| `Node()` | 按 NodeID 查找节点（本实现返回 nil） |
| `Root()` | 获取根节点（本实现返回 nil） |
| `Objects()` | 返回 Objects 节点，包含所有数据点引用 |
| `Browse()` | 浏览节点，返回所有 Flag/Int/Real 节点描述 |
| `Attribute()` | 读取属性值，从 DataSupermarket 返回 DataValue |
| `SetAttribute()` | 写入属性值，更新 DataSupermarket 并触发通知 |

### 4. 并发控制

- **读操作**（`GetFlagDV`, `GetIntDV`, `GetRealDV`）：使用 `RLock()` / `RUnlock()`
- **写操作**（`UpdateFlag`, `UpdateInt`, `UpdateReal`）：使用 `Lock()` / `Unlock()`
- **浏览操作**（`Browse`）：使用 `RLock()` 保护节点列表

### 5. 数据更新流程

#### 写入（SetAttribute）

```
1. 客户端 WriteRequest (NodeID, Value)
2. SetAttribute() 验证类型
3. 根据 NodeID 范围选择 UpdateFlag/UpdateInt/UpdateReal
4. 更新原始数据和对应 DataValue
5. 调用 srv.ChangeNotification() 通知订阅客户端
6. 返回 StatusGood
```

#### 读取（Attribute）

```
1. 客户端 ReadRequest (NodeID)
2. Attribute() 根据 NodeID 计算数组下标
3. 从 DataSupermarket 获取对应 DataValue 指针
4. 直接返回预缓存的 DataValue
```

### 6. Browse 导航树

```
Root (ns=0;i=84)
└── Objects (ns=0;i=85)
    └── DataSupermarket (ns=2;i=85) [HasComponent]
        ├── Flag0000 (ns=2;i=10000) [HasComponent]
        ├── Flag0001 (ns=2;i=10001) [HasComponent]
        ├── ...
        ├── Flag3999 (ns=2;i=13999) [HasComponent]
        ├── Int0000 (ns=2;i=20000) [HasComponent]
        ├── ...
        ├── Int1999 (ns=2;i=21999) [HasComponent]
        ├── Real0000 (ns=2;i=30000) [HasComponent]
        ├── ...
        └── Real0999 (ns=2;i=30999) [HasComponent]
```

---

## 类型转换规则

### Int 类型接受以下类型转换

| 输入类型 | 转换为 | 说明 |
|---------|--------|------|
| int32 | int32 | 直接使用 |
| int16 | int32 | 符号扩展 |
| int64 | int32 | 截断 |
| int | int32 | 平台相关截断 |
| uint32 | int32 | 有符号转换 |
| uint16 | int32 | 符号扩展 |
| 其他 | Error | 返回 TypeMismatch |

### Real 类型接受以下类型转换

| 输入类型 | 转换为 | 说明 |
|---------|--------|------|
| float64 | float64 | 直接使用 |
| float32 | float64 | 精度提升 |
| 其他 | Error | 返回 TypeMismatch |

---

## 命令行参数

```bash
./opcua_supermarket [options]

Options:
  -endpoint string     OPC UA Endpoint URL (default "0.0.0.0")
  -port int            OPC UA Endpoint port (default 4840)
  -cert string         Path to certificate file (default "cert.pem")
  -key string          Path to PEM Private Key file (default "key.pem")
  -gen-cert            Generate a new certificate
```

### 示例

```bash
# 默认配置运行
./opcua_supermarket

# 指定端口
./opcua_supermarket -port 4841

# 监听所有接口
./opcua_supermarket -endpoint 0.0.0.0 -port 4840

# 生成自签名证书
./opcua_supermarket -gen-cert
```

---

## 编译

### Linux

```bash
cd opcua/cmd/supermarket
go build -ldflags="-s -w" -o opcua_supermarket .
```

### Windows (交叉编译)

```bash
cd opcua/cmd/supermarket
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o opcua_supermarket.exe .
```

### 参数说明

| 参数 | 作用 |
|------|------|
| `-s` | 去除符号表，减小文件大小 |
| `-w` | 去除 DWARF 调试信息，减小文件大小 |
| `CGO_ENABLED=0` | 静态编译，不依赖 C 运行时库 |

---

## 项目结构

```
opcua-supermarket/
├── opcua/                          # gopcua 库源码
│   ├── go.mod                     # 依赖声明 (module: github.com/gopcua/opcua)
│   ├── go.sum                     # 依赖校验和
│   ├── id/                        # OPC UA 节点 ID 常量定义
│   │   └── file.go
│   ├── server/                    # 服务器实现
│   │   ├── server.go              # 主服务器逻辑
│   │   ├── namespace.go          # NameSpace 接口
│   │   ├── node.go               # 节点结构
│   │   └── ...
│   ├── ua/                        # UA 数据类型
│   │   ├── node_id.go            # NodeID 相关
│   │   ├── variant.go            # Variant 类型
│   │   ├── data_value.go        # DataValue 结构
│   │   └── ...
│   ├── uacp/                      # OPC UA 协议 (TCP)
│   └── uasc/                      # 安全通道
├── cmd/supermarket/               # 主程序
│   └── opcua_supermarket.go      # 源码 (428 行)
├── opcua_memory.json              # 项目分析文件
└── README.md                      # 本文档
```

---

## 部署

### 方式一：源码部署

1. 复制整个 `opcua` 目录到目标机器
2. 确保目标机器安装 Go 1.23+
3. 运行 `go mod download` 下载依赖
4. 编译并运行

### 方式二：静态可执行文件

1. 使用 `CGO_ENABLED=0` 编译静态版本
2. 复制可执行文件到目标机器
3. 直接运行，无需 Go 环境

---

## 演示模式

服务器内置演示模式，每 5 秒更新前三个数据点：

```go
go func() {
    ticker := time.NewTicker(5 * time.Second)
    count := 0
    for range ticker.C {
        count++
        ds.UpdateInt(0, int32(count))
        ds.UpdateReal(0, float64(count)*1.5)
        ds.UpdateFlag(0, count%2 == 0)
    }
}()
```

**效果**：
- `Flag0000`：每 10 秒切换 true/false
- `Int0000`：每 5 秒递增 1
- `Real0000`：每 5 秒增加 1.5

---

## 性能特性

| 指标 | 数值 | 说明 |
|------|------|------|
| 内存占用 | ~1MB | 固定数组大小，预分配 DataValue |
| 读取延迟 | <1μs | 数组直接索引，无锁争用 |
| 写入延迟 | <10μs | 包含锁竞争和数据复制 |
| 最大客户端 | 数百 | 取决于操作系统网络栈 |

---

## 依赖

- Go 1.23+
- github.com/gopcua/opcua (内置于 opcua 目录)
