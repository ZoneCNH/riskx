// Package riskx 实现事前风控引擎（最小可用实现，P0 ③-c）。
//
// 职责：消费 signal_factory 输出的 []contracts.SignalIntent，
// 经仓位检查和熔断门禁后，输出已批准的 []ApprovedIntent。
//
// 约束（P0 最小实现）：
//   - 单仓位上限：MaxPositionPct（默认 10%）
//   - 最大同时持仓数：MaxPositions（默认 5）
//   - 熔断：日内亏损超 DrawdownLimit（默认 5%）时拒绝所有新开仓
//   - 无状态持久化；重启后仓位归零（待 P1 对接 postgresx/redisx）
package riskx

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ZoneCNH/contracts/pkg/contracts"
	"github.com/google/uuid"
)

// Config 风控参数。
type Config struct {
	// MaxPositionPct 单仓位最大占 NAV 比例（0~1），默认 0.10。
	MaxPositionPct float64
	// MaxPositions 允许同时持有的最大仓位数，默认 5。
	MaxPositions int
	// DrawdownLimit 日内最大回撤比例（0~1），超过触发熔断，默认 0.05。
	DrawdownLimit float64
}

// DefaultConfig 返回保守默认配置。
func DefaultConfig() Config {
	return Config{
		MaxPositionPct: 0.10,
		MaxPositions:   5,
		DrawdownLimit:  0.05,
	}
}

// ApprovedIntent 经风控通过的信号意图，含最终尺寸。
type ApprovedIntent struct {
	ID         string                  `json:"id"`
	ApprovedAt int64                   `json:"approved_at"`
	Intent     contracts.SignalIntent  `json:"intent"`
	FinalSize  float64                 `json:"final_size"` // 实际仓位比例（≤ MaxPositionPct）
}

// RejectedIntent 被风控拒绝的信号意图，含拒绝原因。
type RejectedIntent struct {
	Intent contracts.SignalIntent `json:"intent"`
	Reason string                 `json:"reason"`
}

// Result 单次 Evaluate 的结果。
type Result struct {
	Approved []ApprovedIntent `json:"approved"`
	Rejected []RejectedIntent `json:"rejected"`
}

// Engine 风控引擎。线程安全。
type Engine struct {
	cfg Config

	mu          sync.Mutex
	positions   map[string]float64 // symbol → current size_pct
	circuitOpen bool               // 熔断状态
	circuitAt   time.Time          // 熔断触发时间
	drawdown    float64            // 当日累计亏损（占 NAV 比例，正数表示亏损）
}

// New 创建风控引擎。
func New(cfg Config) *Engine {
	if cfg.MaxPositionPct <= 0 {
		cfg.MaxPositionPct = DefaultConfig().MaxPositionPct
	}
	if cfg.MaxPositions <= 0 {
		cfg.MaxPositions = DefaultConfig().MaxPositions
	}
	if cfg.DrawdownLimit <= 0 {
		cfg.DrawdownLimit = DefaultConfig().DrawdownLimit
	}
	return &Engine{
		cfg:       cfg,
		positions: make(map[string]float64),
	}
}

// Evaluate 对 []SignalIntent 执行风控检查，返回 Result。
//
// 规则（按优先级）：
//  1. 熔断开路：拒绝所有仓位 SizePct > 0 的意图（允许平仓 SizePct=0）
//  2. 仓位上限：intent.SizePct > MaxPositionPct → 截断为 MaxPositionPct
//  3. 最大持仓数：当前持仓数 ≥ MaxPositions 且该 symbol 无现有仓位 → 拒绝
func (e *Engine) Evaluate(_ context.Context, intents []contracts.SignalIntent) Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := Result{
		Approved: make([]ApprovedIntent, 0, len(intents)),
		Rejected: make([]RejectedIntent, 0),
	}

	for _, intent := range intents {
		reason, finalSize := e.check(intent)
		if reason != "" {
			result.Rejected = append(result.Rejected, RejectedIntent{
				Intent: intent,
				Reason: reason,
			})
			continue
		}
		e.positions[intent.Symbol] = finalSize
		result.Approved = append(result.Approved, ApprovedIntent{
			ID:         uuid.New().String(),
			ApprovedAt: time.Now().UnixMilli(),
			Intent:     intent,
			FinalSize:  finalSize,
		})
	}
	return result
}

// check 执行单个 SignalIntent 的风控检查。
// 返回 (拒绝原因, 最终尺寸)；拒绝原因为空表示通过。
func (e *Engine) check(intent contracts.SignalIntent) (reason string, finalSize float64) {
	// 熔断检查：仅拒绝新开仓（SizePct > 0）
	if e.circuitOpen && intent.SizePct > 0 {
		return fmt.Sprintf("circuit breaker open since %s (drawdown=%.2f%%)",
			e.circuitAt.Format(time.RFC3339), e.drawdown*100), 0
	}

	// 平仓信号（ActionE 或 SizePct=0）直接通过
	if intent.SizePct <= 0 || intent.Action == contracts.ActionE {
		return "", 0
	}

	// 仓位上限截断
	size := intent.SizePct
	if size > e.cfg.MaxPositionPct {
		size = e.cfg.MaxPositionPct
	}

	// 最大持仓数检查（仅新仓位）
	_, hasPosition := e.positions[intent.Symbol]
	if !hasPosition && len(e.positions) >= e.cfg.MaxPositions {
		return fmt.Sprintf("max positions (%d) reached", e.cfg.MaxPositions), 0
	}

	return "", size
}

// TriggerCircuitBreaker 触发熔断（外部调用，例如 P&L 监控检测到超额回撤）。
func (e *Engine) TriggerCircuitBreaker(drawdown float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.circuitOpen = true
	e.circuitAt = time.Now().UTC()
	e.drawdown = drawdown
}

// ResetCircuitBreaker 重置熔断（日初或人工复位）。
func (e *Engine) ResetCircuitBreaker() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.circuitOpen = false
	e.drawdown = 0
}

// CircuitOpen 返回熔断状态。
func (e *Engine) CircuitOpen() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.circuitOpen
}

// PositionCount 返回当前持仓数量。
func (e *Engine) PositionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.positions)
}

// ClearPositions 清空所有仓位（日初重置或测试用）。
func (e *Engine) ClearPositions() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.positions = make(map[string]float64)
}
