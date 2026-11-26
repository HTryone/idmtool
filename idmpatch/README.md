# IDMPatch — IDM 6.43 Build 18 激活方案（C · GUI）

> **第二版（激活方案，C 语言）**。与第一版 `idmGO\`（Go · 冻结注册表）**并行**，互不替代。
>
> 交付 **两个文件**：
> | 文件 | 类型 | 职责 |
> |---|---|---|
> | `idmtool.exe` | **GUI**（Windows 子系统，无黑窗，带 IDM 图标）| 一键安装/激活、还原、查看状态、一次性激活、生成序列号 |
> | `idmquiet.exe` | **常驻守护**（无窗口，单独进程，开机自启）| 自动关提示框 + 阻止打开购买页 |

## 使用（GUI）

把 `idmtool.exe` 和 `idmquiet.exe` 放同一目录，双击 `idmtool.exe`（自动请求管理员）：
- **一键安装 / 激活**：备份原版 → 给 IDMan.exe 打补丁 → 写注册信息 → 部署 `idmquiet.exe`（到 `%LOCALAPPDATA%\idmfix`）+ 开机自启 → 启动 IDM。
- **还原原版**：从备份恢复 IDMan.exe，删除 idmquiet 自启。
- **查看状态**：显示补丁是否齐全、备份、守护、自启。
- **一次性激活（对话框）**：走 IDM 官方注册框激活（需原版 exe）。
- **生成序列号**：按逆向算法生成合法号。

= 操作成功后「关于」框显示 **版本 … Full** + **此产品授权给：HTryone Hu**。

## 两个程序的分工（为什么拆两个）
- `idmtool.exe` 是一次性操作工具（装/还原/看状态），做完即可关。
- `idmquiet.exe` 是**常驻**守护，生命周期长：① 关掉 IDM 反篡改提示「cracked product / 以管理员身份」；② 秒杀 IDM 拉起的浏览器子进程 → **不再跳购买页**。它单独成文件，开机自启，不随面板关闭而退出。

## 核心：IDMan.exe 字节补丁（idmtool.exe 生成）
| 类别 | 说明 |
|---|---|
| 跳过许可校验 | `0x05190A: e821e1ffff -> 31c0909090`（不联网、不弹注册框）|
| 显示 Full | `0x2D8A90 / 0x2E30B3 / 0x2FBB85: Trial -> Full ` |
| lic_flag=0 | 文件 `0x3C3724: 01 -> 00`（已注册态）|
| **禁止跳浏览器** | **20 处 `open + ShellExecuteA` 调用点全 NOP**（`ff1544e57c00 -> 909090909090`），彻底不打开购买/官网页面 |
| 清购买 URL | `buy.html / buy1.html / welcome.html / welcome2.html / get_prices_for_app.html` 等全部清空 |

补丁版 IDMan.exe 由 `idmtool.exe` 从原版备份生成（可复现）。原版备份：`work\IDMan.exe.orig-backup`。

## 源码（`src\`）
- `idmtool.c` — GUI 工具（含 keygen/补丁/安装/还原/状态/激活全部逻辑）
- `idmquiet.c` — 常驻守护（关框 + 杀购买页浏览器子进程）
- `app.rc`（`1 ICON "idmfrost.ico"`）+ `idmfrost.ico` — 嵌入 IDM 图标
- 高 DPI 感知（Per-Monitor V2 动态调用）+ 微软雅黑 UI 字体

## 编译（32 位，gcc = `D:\Toolchain32\mingw32\bin\gcc.exe`）
```
cd src
windres -i app.rc -o app_res.o
cd ..
gcc -O2 -municode -mwindows -o bin\idmtool.exe  src\idmtool.c  src\app_res.o -lcomctl32 -lgdi32 -luser32 -ladvapi32 -lshell32
gcc -O2 -municode -mwindows -o bin\idmquiet.exe src\idmquiet.c  -luser32
```

## 逆向要点
- 序列号：字符集 `2YOPB3AQCVUXMNRS97WE0IZD4KLFGHJ8165T`，`v=v*37+index`，模 43/23/17/53，4 段×5 字符。
- `lic_flag [0x7C4D24]`（.data file `0x3C3724`）1=试用 0=已注册。
- `0x450630` 许可检查（唯一调用点 `0x45250A`）→ 跳转表 `0x4534BC`：3=假冒弹窗、5/8/9/11=正常。跳过该调用即离线已注册。
- 联网校验真实请求：`POST /data/<id>/register.cgi`，`Host: www.internetdownloadmanager.com`，body `IDM-DataType-a1:<base64 密文>`；响应 64 字节分组密文（服务端白名单，客户端无法伪造有效）。

## 已知边界
- **启动/静置**：完全干净（无弹框、不跳购买页、不联网），lic_flag=0，About 显示 Full+姓名。
- **点「下载」动作**：IDM build18 会做一次**在线许可复检**，keygen 号被服务器判假可能弹框。**这与补丁无关**——原版走官方注册框真激活后下载同样触发（服务端白名单墙）。彻底免疫需真实正版序列号。

## 免责声明
仅供研究学习。IDM 为 Tonec Inc. 商业软件，请支持正版：https://www.internetdownloadmanager.com/buy_now.html
