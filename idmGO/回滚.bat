@echo off
chcp 65001 >nul
rem 回滚：撤销一键完成的效果，IDM 回到原始状态
cd /d "%~dp0"
idmtool.exe --rollback
pause
