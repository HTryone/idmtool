package idmtool

// 备份与读取辅助。备份 = reg.exe export（reg.exe 无直接 API 等价物且要 .reg 文件格式，
// fork 一次可接受；输出重定向到日志）。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"idmtool/src/system"
)

// backupCLSID 导出 CLSID 键到 %SystemRoot%\Temp\_Backup_*.reg。
func backupCLSID(e *Env) error {
	if TestMode {
		Step("  [测试] 跳过备份")
		return nil
	}
	tempDir := filepath.Join(os.Getenv("SystemRoot"), "Temp")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return err
	}
	ts := time.Now().Format("20060102-150405")
	_ = regExport(`HKCU\`+e.CLSIDSub, filepath.Join(tempDir, fmt.Sprintf("_Backup_HKCU_CLSID_%s.reg", ts)))
	if !e.HKCUsync {
		_ = regExport(`HKU\`+e.SID+`\`+e.CLSID2Sub,
			filepath.Join(tempDir, fmt.Sprintf("_Backup_HKU-%s_CLSID_%s.reg", e.SID, ts)))
	}
	Step("  备份已写入 %s", tempDir)
	return nil
}

func regExport(keyPath, file string) error {
	cmd := exec.Command("reg.exe", "export", keyPath, file, "/y")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg export %s: %v: %s", keyPath, err, string(out))
	}
	return nil
}

// readStr 读当前用户（HKCU 或 HKU\<sid>）DownloadManager 下的字符串值。
func readStr(e *Env, base, name string) (string, bool) {
	if v, ok := system.RegReadString(syscall.HKEY_CURRENT_USER, base, name); ok {
		return v, true
	}
	return system.RegReadString(syscall.HKEY_USERS, e.SID+`\`+base, name)
}
