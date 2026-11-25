package idmtool

// 失败熔断：同一时段连续失败达上限就停手，避免空转烧资源。
//
// 规则（写进 config.json，重启也不丢）：
//   - 每失败一次计数 +1，并记下首次失败时间
//   - 达到 maxConsecutiveFail 次 → 进入冷却 cooldownHours 小时
//     冷却期内 daemonTick 直接返回，不启 IDM、不锁键、不写重复日志
//   - 冷却结束后允许再试一次；成功则清零，再失败则重新计
//
// 这样最坏情况也只是"每 6 小时试一次、连试 3 次后歇 6 小时"，
// 不会出现"每 5 分钟疯狂重试"把 CPU/磁盘打满。

import (
	"time"
)

const (
	maxConsecutiveFail = 3                // 连续失败上限
	cooldownHours      = 6                // 熔断冷却时长
)

// failureState 记录在 Config 里，跨进程存活。
type failureState struct {
	ConsecutiveFail int    `json:"consecutive_fail"`
	FirstFailAt     string `json:"first_fail_at"`
}

// RecordFailure 记一次失败（写配置文件）。
func RecordFailure(reason string) {
	c := LoadConfig()
	c.Fail.ConsecutiveFail++
	if c.Fail.FirstFailAt == "" {
		c.Fail.FirstFailAt = time.Now().Format("2006-01-02 15:04:05")
	}
	_ = SaveConfig(c)
	// 记一条错误日志（用文件日志，避免污染界面）
	FileLogger()("✗ 失败 #%d（%s）：%s", c.Fail.ConsecutiveFail, reason, c.Fail.FirstFailAt)
}

// ResetFailureState 成功时清零。
func ResetFailureState() {
	c := LoadConfig()
	if c.Fail.ConsecutiveFail == 0 {
		return
	}
	c.Fail.ConsecutiveFail = 0
	c.Fail.FirstFailAt = ""
	_ = SaveConfig(c)
}

// inFailureCooldown 是否在冷却期（连续失败达上限且未超冷却时长）。
func inFailureCooldown() bool {
	c := LoadConfig()
	if c.Fail.ConsecutiveFail < maxConsecutiveFail {
		return false
	}
	if c.Fail.FirstFailAt == "" {
		return false
	}
	t0, err := time.ParseInLocation("2006-01-02 15:04:05", c.Fail.FirstFailAt, time.Local)
	if err != nil {
		return false
	}
	// 冷却期内：不再尝试
	return time.Since(t0) < time.Duration(cooldownHours)*time.Hour
}

// CooldownRemaining 给状态页展示：还有多久解除熔断。
func CooldownLeft() time.Duration {
	c := LoadConfig()
	if c.Fail.ConsecutiveFail < maxConsecutiveFail || c.Fail.FirstFailAt == "" {
		return 0
	}
	t0, err := time.ParseInLocation("2006-01-02 15:04:05", c.Fail.FirstFailAt, time.Local)
	if err != nil {
		return 0
	}
	left := time.Duration(cooldownHours)*time.Hour - time.Since(t0)
	if left < 0 {
		return 0
	}
	return left
}

