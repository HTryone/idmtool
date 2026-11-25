@echo off
setlocal enabledelayedexpansion
chcp 65001 >nul
rem 一键构建：源码在 src\，产物输出到 ..\dist\
rem   idmfrost.exe  = 界面 + 激活
rem   idmguard.exe  = 纯守护（无窗口）
rem 两个 exe 共用同一个图标（assets\idmfrost.ico），各自生成一份 rsrc.syso。
cd /d "%~dp0"

where go >nul 2>nul || (
    echo [x] 未找到 Go，请先安装：https://go.dev/dl/
    pause
    exit /b 1
)

rem 产物目录
set "DIST=..\dist"
if not exist "%DIST%" mkdir "%DIST%"

set "RSRC=%USERPROFILE%\go\bin\rsrc.exe"

rem 1) 图标资源：两个包各生成一份（同一个 ico → 图标完全一致）
if not exist "src\rsrc.syso" (
    if exist "!RSRC!" (
        echo [i] 生成主程序图标资源 ...
        "!RSRC!" -ico "assets\idmfrost.ico" -o "src\rsrc.syso" -arch amd64
    ) else (
        echo [!] 未找到 rsrc，主程序将不带图标。安装：go install github.com/akavel/rsrc@latest
    )
)
if not exist "src\guard\rsrc.syso" (
    if exist "!RSRC!" (
        echo [i] 生成守护程序图标资源 ...
        "!RSRC!" -ico "assets\idmfrost.ico" -o "src\guard\rsrc.syso" -arch amd64
    ) else (
        echo [!] 未找到 rsrc，守护程序将不带图标。安装：go install github.com/akavel/rsrc@latest
    )
)

rem 2) 构建界面+激活（无控制台窗口）
go build -trimpath -ldflags "-s -w -H=windowsgui" -o "%DIST%\idmfrost.exe" ./src || (
    echo [x] idmfrost.exe 构建失败
    pause
    exit /b 1
)
echo [ok] %DIST%\idmfrost.exe 已生成

rem 3) 构建纯守护（无控制台窗口）
go build -trimpath -ldflags "-s -w -H=windowsgui" -o "%DIST%\idmguard.exe" ./src/guard || (
    echo [x] idmguard.exe 构建失败
    pause
    exit /b 1
)
echo [ok] %DIST%\idmguard.exe 已生成

echo.
echo 用法：把 idmfrost.exe 与 idmguard.exe 放在同一目录，双击 idmfrost.exe → 点「一键完成」
pause
