# 元宝龙虾集成 Skill

## 概述

将元宝 App 与 OpenClaw 原生集成，实现元宝与 OpenClaw 的双向通信。

## 集成流程

### 步骤 1：安装元宝龙虾插件

在已部署 OpenClaw 的设备上运行：

```bash
openclaw plugins install "openclaw-plugin-yuanbao@latest"
```

### 步骤 2：配置元宝通道

1. 打开元宝 App
2. 进入「关联已有 OpenClaw」面板
3. 选择「方式2：通道配置」
4. 复制页面中的 AppID 和 AppSecret
5. 运行配置命令：

```bash
openclaw channels add --channel yuanbao --token "<你的AppID>:<你的AppSecret>"
```

### 步骤 3：重启 OpenClaw 服务

```bash
openclaw gateway restart
```

### 步骤 4：完成关联

在元宝 App 中点击「我已操作」完成关联。

## 配置参数说明

| 参数 | 说明 | 来源 |
|------|------|------|
| AppID | 元宝应用标识 | 元宝 App 配置面板 |
| AppSecret | 元宝应用密钥 | 元宝 App 配置面板 |

## 常见问题

### Q: 插件安装失败？
检查网络连接，确保能访问 npm 仓库。

### Q: 通道配置失败？
确认 AppID 和 AppSecret 格式正确，用冒号分隔。

### Q: 重启后仍无法使用？
检查 OpenClaw 日志，确认插件加载成功。

## 技术细节

- **插件名称**: `openclaw-plugin-yuanbao`
- **通道类型**: `yuanbao`
- **认证方式**: AppID + AppSecret

## 相关命令

```bash
# 查看已安装插件
openclaw plugins list

# 查看通道配置
openclaw channels list

# 移除元宝通道
openclaw channels remove --channel yuanbao
```

## 注意事项

1. AppSecret 需妥善保管，不要泄露
2. 重启服务后需要等待几秒让插件完全加载
3. 如需多设备绑定，每个设备需要独立的 AppID/AppSecret
