package riskx_test

import (
	"context"
	"testing"

	"github.com/ZoneCNH/contracts/pkg/contracts"
	"github.com/ZoneCNH/riskx/pkg/riskx"
)

func intent(symbol string, action contracts.Action, sizePct float64) contracts.SignalIntent {
	return contracts.SignalIntent{
		ID:      symbol + "-sig",
		CardID:  "card-001",
		Symbol:  symbol,
		Action:  action,
		SizePct: sizePct,
	}
}

// TestEvaluate_NormalApproval: 正常信号通过。
func TestEvaluate_NormalApproval(t *testing.T) {
	e := riskx.New(riskx.DefaultConfig())
	res := e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionA, 0.08),
	})
	if len(res.Approved) != 1 {
		t.Fatalf("approved: got %d want 1", len(res.Approved))
	}
	if res.Approved[0].FinalSize != 0.08 {
		t.Errorf("FinalSize: got %f want 0.08", res.Approved[0].FinalSize)
	}
}

// TestEvaluate_CapAtMaxPositionPct: SizePct 超上限截断为 MaxPositionPct。
func TestEvaluate_CapAtMaxPositionPct(t *testing.T) {
	e := riskx.New(riskx.DefaultConfig()) // MaxPositionPct=0.10
	res := e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionA, 0.25), // 超过 10%
	})
	if len(res.Approved) != 1 {
		t.Fatalf("approved: got %d want 1", len(res.Approved))
	}
	if res.Approved[0].FinalSize != 0.10 {
		t.Errorf("FinalSize: got %f want 0.10 (capped)", res.Approved[0].FinalSize)
	}
}

// TestEvaluate_MaxPositionsRejection: 持仓满时新 symbol 被拒。
func TestEvaluate_MaxPositionsRejection(t *testing.T) {
	cfg := riskx.Config{MaxPositionPct: 0.10, MaxPositions: 2, DrawdownLimit: 0.05}
	e := riskx.New(cfg)

	e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionA, 0.08),
		intent("ETHUSDT", contracts.ActionA, 0.08),
	})
	// 第三个新 symbol 应被拒
	res := e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("SOLUSDT", contracts.ActionA, 0.05),
	})
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected: got %d want 1", len(res.Rejected))
	}
}

// TestEvaluate_CircuitBreakerRejectsNewPositions: 熔断后拒绝新开仓。
func TestEvaluate_CircuitBreakerRejectsNewPositions(t *testing.T) {
	e := riskx.New(riskx.DefaultConfig())
	e.TriggerCircuitBreaker(0.06)

	res := e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionA, 0.08),
	})
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected: got %d want 1", len(res.Rejected))
	}
}

// TestEvaluate_CircuitBreakerAllowsClosePositions: 熔断后允许平仓（SizePct=0）。
func TestEvaluate_CircuitBreakerAllowsClosePositions(t *testing.T) {
	e := riskx.New(riskx.DefaultConfig())
	e.TriggerCircuitBreaker(0.06)

	res := e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionE, 0.0),
	})
	if len(res.Approved) != 1 {
		t.Fatalf("close position should be approved even during circuit break; got %d rejected", len(res.Rejected))
	}
}

// TestEvaluate_ResetCircuitBreaker: 重置熔断后正常通过。
func TestEvaluate_ResetCircuitBreaker(t *testing.T) {
	e := riskx.New(riskx.DefaultConfig())
	e.TriggerCircuitBreaker(0.06)
	e.ResetCircuitBreaker()

	res := e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionA, 0.08),
	})
	if len(res.Approved) != 1 {
		t.Fatalf("approved after reset: got %d want 1", len(res.Approved))
	}
}

// TestEvaluate_ExistingSymbolBypassMaxPositions: 已有仓位的 symbol 更新不受 MaxPositions 限制。
func TestEvaluate_ExistingSymbolBypassMaxPositions(t *testing.T) {
	cfg := riskx.Config{MaxPositionPct: 0.10, MaxPositions: 1, DrawdownLimit: 0.05}
	e := riskx.New(cfg)

	// 先占满 1 个仓位
	e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionA, 0.08),
	})
	// 更新同一 symbol 应通过
	res := e.Evaluate(context.Background(), []contracts.SignalIntent{
		intent("BTCUSDT", contracts.ActionB, 0.05),
	})
	if len(res.Approved) != 1 {
		t.Fatalf("existing symbol update should pass; rejected: %v", res.Rejected)
	}
}
