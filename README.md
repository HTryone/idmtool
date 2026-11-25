# IDMFrost — IDM 试用守护

一键冻结 IDM 试用期并自动维持。**两个 exe 各司其职：界面/激活一个，守护一个。**

![icon](idmGO/assets/idmfrost.png)

## 两个程序

| 文件 | 职责 | 说明 |
|---|---|---|
| `idmfrost.exe` | **界面 + 激活** | 双击打开，点「一键完成 / 回滚」，操作完可关闭 |
| `idmguard.exe` | **后台守护** | 无窗口、单实例，常驻补锁；由「一键完成」拉起并装开机自启 |

两个文件必须放在**同一目录**。图标完全一致。

## 快速使用

1. 构建或拿到 `idmfrost.exe` + `idmguard.exe`（放同一目录）
2. 双击 `idmfrost.exe` → UAC 点「是」
3. 点 **「一键完成」** → 等 10 秒 → 完事

之后 IDM 一直可用：激活完成时后台守护已立即启动，并写好开机自启，重启后自动接着守护，无需再打开本工具。想恢复原样点「回滚」（会连后台守护一起停掉）。

## 它做了什么

IDM（6.43 实测）判断试用过期看的是注册表里一个隐藏的 CLSID 键：

```
HKCU\Software\Classes\WOW6432Node\CLSID\{07999AC3-058B-40BF-984F-69EB1E554CA7}
    Model = 328
```

IDMFrost 用 Deny ACL 把这个键锁死，IDM 读不到 → 永远按「未过期」处理。
独立的 `idmguard.exe` 常驻后台定期检查，IDM 自动更新若重写了权限就自动补锁。

## 进程程序与激活程序分离

激活是一次性动作，守护是长期常驻——两者目标不同、生命周期不同，分开更稳：

- **`idmfrost.exe`**：只在你双击的那段时间存在。点「一键完成」后它会
  ① 冻结试用键 ② 把守护写进开机启动文件夹 ③ **立即拉起一次 `idmguard.exe`**，然后你可以直接关掉界面。
- **`idmguard.exe`**：纯守护，无窗口、`Global\` 命名互斥保证**单实例**（重复启动会自动退出）。
  它按周期补锁，被回滚或用户手动结束时才停。

数据目录在 exe 同级 `data\`：

```
dist\
├── idmfrost.exe          界面 + 激活
├── idmguard.exe          后台守护
└── data\
    ├── config.json       配置（enabled / installed_at / last_seen）
    └── log\
        └── 2026-11-25.log  按天一个，仅保留最近 5 天
```

守护只在 `config.json` 里 `enabled=true`（=用户点过「一键完成」）时才工作；
条件不成立就完全静默，不碰注册表、不启 IDM、不报错。

## 实测证据

| 实验 | 结果 |
|---|---|
| 用用户权限写锁死的键 | 拒绝（错误码 5） |
| 用用户权限删锁死的键 | 拒绝，TakeOwnership 都打不开 |
| 连续启动 IDM 8 次 | 锁始终有效，无过期弹窗 |
| `radxcnt` 改 999/9999 | 依然正常 → 不是判定依据 |
| 清键后仅启动 IDM | 键自动重建 → 不需要触发下载 |

## 构建

```
双击 idmGO\build.bat  →  dist\idmfrost.exe + dist\idmguard.exe
```

需要 Go，可选 `rsrc`（生成图标；两个 exe 共用 `assets\idmfrost.ico`，图标完全一致）。

## 文档

详见 [idmGO/README.md](idmGO/README.md)（完整原理、实现坑、验证记录）。

## 免责声明

不修改 IDM 程序文件，只操作注册表；执行前自动备份到 `%SystemRoot%\Temp`。
Internet Download Manager 是 Tonec Inc. 的商业软件，本项目与其无关联。
请支持正版：https://www.internetdownloadmanager.com/buy_now.html

