@echo off
chcp 65001 >nul
title 修复 IDM 每日提示乱码（GBK -> UTF-8）
cd /d "%~dp0"

net session >nul 2>&1
if %errorlevel% NEQ 0 (
    echo 需要管理员权限。请在 UAC 弹窗中点击“是”。
    echo 若看不到弹窗，请检查任务栏闪烁的盾牌图标。
    powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
    exit /b
)

echo 已获得管理员权限，正在修复...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0fix_idm_tips.ps1"
echo.
echo 完成。请重新打开 IDM 的“每日提示”查看效果。
pause
