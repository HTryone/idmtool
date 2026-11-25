package idmtool

// 配置状态（不用注册表）：记录"守护是否已启用（用户点过一键完成）"等。
//
// 为什么用文件不用注册表：
//   - 注册表是 IDM 自己的地盘，我们只需要记录自己的开关，放文件更干净；
//   - 文件可读可查可删，出问题用户自己就能看/改；
//   - 卸载直接删目录即可，不留注册表残留。
//
// 位置：%LOCALAPPDATA%\idmtool\config.json（每用户，随用户走，不随 exe 移动）。
// 写入时机：程序"启动后就写"——每次启动都刷新 LastSeen，顺带确保目录/文件存在。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config 守护配置。
type Config struct {
	Enabled     bool         `json:"enabled"`      // 用户点过「一键完成」→ true；回滚 → false
	InstalledAt string       `json:"installed_at"` // 首次启用时间
	LastSeen    string       `json:"last_seen"`    // 最近一次程序启动时间
	ExePath     string       `json:"exe_path"`     // 记录当前程序路径（守护自愈用）
	Fail        failureState `json:"fail"`         // 失败熔断状态
}

// DataDir 数据目录 = exe 同级 data\ （配置与日志都在这里）。
// 目录不可写时回退 %LOCALAPPDATA%\idmtool\data。
func DataDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "data")
		if e := os.MkdirAll(dir, 0o755); e == nil {
			if w, e2 := os.Create(filepath.Join(dir, ".wtest")); e2 == nil {
				w.Close()
				os.Remove(filepath.Join(dir, ".wtest"))
				return dir
			}
		}
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "idmtool", "data")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// configPath 配置文件完整路径：data\config.json
func configPath() string { return filepath.Join(DataDir(), "config.json") }

// LoadConfig 读配置；文件不存在/损坏都返回空配置（Enabled=false）。
func LoadConfig() *Config {
	c := &Config{}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, c)
	return c
}

// SaveConfig 写配置（原子替换，避免写一半损坏）。
func SaveConfig(c *Config) error {
	if TestMode {
		return nil
	}
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := configPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	_ = os.Remove(configPath())
	if err := os.Rename(tmp, configPath()); err != nil {
		return err
	}
	return nil
}

// TouchConfig 程序启动时调用：确保配置存在并刷新 LastSeen（"启动后就写"）。
// 不改变 Enabled —— 只做心跳，不篡改用户的启用状态。
func TouchConfig() {
	c := LoadConfig()
	exe, _ := os.Executable()
	c.LastSeen = time.Now().Format("2006-01-02 15:04:05")
	if exe != "" {
		c.ExePath = exe
	}
	if c.Enabled && c.InstalledAt == "" {
		c.InstalledAt = c.LastSeen
	}
	_ = SaveConfig(c)
}

// EnableConfig 一键完成时调用：置 Enabled=true 并记录时间。
func EnableConfig() error {
	c := LoadConfig()
	if !c.Enabled {
		c.Enabled = true
		c.InstalledAt = time.Now().Format("2006-01-02 15:04:05")
	}
	exe, _ := os.Executable()
	if exe != "" {
		c.ExePath = exe
	}
	c.LastSeen = time.Now().Format("2006-01-02 15:04:05")
	return SaveConfig(c)
}

// DisableConfig 回滚时调用：置 Enabled=false。
func DisableConfig() error {
	c := LoadConfig()
	c.Enabled = false
	c.InstalledAt = ""
	c.LastSeen = time.Now().Format("2006-01-02 15:04:05")
	return SaveConfig(c)
}

// DaemonEnabled 守护是否应工作（= 用户点过一键完成）。
func DaemonEnabled() bool {
	if TestMode {
		return false
	}
	return LoadConfig().Enabled
}

// ConfigSummary 给状态页展示用。
func ConfigSummary() string {
	c := LoadConfig()
	if !c.Enabled {
		return "未启用（未点过「一键完成」）"
	}
	return fmt.Sprintf("已启用（自 %s，最近运行 %s）", c.InstalledAt, c.LastSeen)
}
