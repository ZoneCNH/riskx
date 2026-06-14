# riskx

`riskx` 是执行域的风控引擎，负责对所有订单进行事前风控检查、执行仓位限额控制、监控回撤和触发熔断。

## 定位

riskx 是订单进入交易所前的最后一道门禁。架构规则：策略只能通过 riskx 提交订单。

## 核心内容

- **事前风控**: 仓位限额、单笔限额、频率限制
- **回撤控制**: 触发熔断 → 滞后恢复
- **Kill Switch**: 紧急停止所有交易
- **风险指标**: VaR、Sharpe、Max Drawdown

## 规格

完整模块规格见 [module/riskx/SPEC.md](https://github.com/ZoneCNH/ZoneCNH/blob/main/module/riskx/SPEC.md)。