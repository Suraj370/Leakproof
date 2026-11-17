package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"leakproof/internal/cards"
	"leakproof/internal/mf"
)

// Load reads the current reference data: every fund with its LATEST disclosed holdings, and the
// currently effective version of every card with its category rules.
func Load(ctx context.Context, pool *pgxpool.Pool) (*Catalog, error) {
	c := &Catalog{Funds: map[string]mf.Fund{}}

	rows, err := pool.Query(ctx, `
		SELECT isin, name, plan::text, ter_bps, COALESCE(direct_ter_bps, ter_bps), exit_load_bps, exit_load_days, equity_oriented
		FROM mutual_funds`)
	if err != nil {
		return nil, fmt.Errorf("funds: %w", err)
	}
	for rows.Next() {
		var f mf.Fund
		var plan string
		if err := rows.Scan(&f.ISIN, &f.Name, &plan, &f.TERBps, &f.DirectTERBps, &f.ExitLoadBps, &f.ExitLoadDays, &f.EquityOriented); err != nil {
			rows.Close()
			return nil, fmt.Errorf("funds scan: %w", err)
		}
		f.ISIN = strings.TrimSpace(f.ISIN)
		f.Plan = mf.PlanType(plan)
		f.Holdings, f.HoldingNames = map[string]int64{}, map[string]string{}
		c.Funds[f.ISIN] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `
		SELECT f.isin, h.stock_isin, h.stock_name, h.weight_bps
		FROM fund_holdings h JOIN mutual_funds f ON f.id = h.fund_id
		WHERE h.as_of_month = (SELECT max(h2.as_of_month) FROM fund_holdings h2 WHERE h2.fund_id = h.fund_id)`)
	if err != nil {
		return nil, fmt.Errorf("holdings: %w", err)
	}
	for rows.Next() {
		var fundISIN, stockISIN, name string
		var w int64
		if err := rows.Scan(&fundISIN, &stockISIN, &name, &w); err != nil {
			rows.Close()
			return nil, fmt.Errorf("holdings scan: %w", err)
		}
		f, ok := c.Funds[strings.TrimSpace(fundISIN)]
		if !ok {
			continue
		}
		stockISIN = strings.TrimSpace(stockISIN)
		f.Holdings[stockISIN], f.HoldingNames[stockISIN] = w, name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	byID := map[int64]*cards.Card{}
	var order []int64
	rows, err = pool.Query(ctx, `
		SELECT DISTINCT ON (slug) id, slug, name, annual_fee_paise, fee_waiver_spend_paise, base_earn_bps, redeem_paise_per_point
		FROM credit_cards
		WHERE effective_from <= current_date AND (effective_to IS NULL OR effective_to > current_date)
		ORDER BY slug, version DESC`)
	if err != nil {
		return nil, fmt.Errorf("cards: %w", err)
	}
	for rows.Next() {
		var id int64
		cd := cards.Card{Rules: map[cards.Category]cards.CategoryRule{}, GroupCapPoints: map[string]int64{}}
		if err := rows.Scan(&id, &cd.ID, &cd.Name, &cd.AnnualFeePaise, &cd.FeeWaiverSpendPaise, &cd.BaseEarnBps, &cd.RedeemPaisePerPoint); err != nil {
			rows.Close()
			return nil, fmt.Errorf("cards scan: %w", err)
		}
		byID[id] = &cd
		order = append(order, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `
		SELECT card_id, category::text, earn_bps, excluded, COALESCE(cap_group, ''), COALESCE(monthly_cap_points, 0)
		FROM card_category_rules`)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	for rows.Next() {
		var id, capPts int64
		var cat, group string
		var r cards.CategoryRule
		if err := rows.Scan(&id, &cat, &r.EarnBps, &r.Excluded, &group, &capPts); err != nil {
			rows.Close()
			return nil, fmt.Errorf("rules scan: %w", err)
		}
		cd, ok := byID[id]
		if !ok {
			continue // rule belongs to a superseded card version
		}
		r.CapGroup = group
		cd.Rules[cards.Category(cat)] = r
		if group != "" {
			cd.GroupCapPoints[group] = max(cd.GroupCapPoints[group], capPts)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range order {
		c.Cards = append(c.Cards, *byID[id])
	}
	return c, nil
}
