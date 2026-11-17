package ingest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leakproof/internal/cards"
)

var (
	ErrUnknownFund = errors.New("fund is not in mutual_funds (import it with the funds command first)")
	ErrBigChange   = errors.New("holdings differ too much from the previous month (use --force if the fund really restructured)")
)

func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpsertFunds inserts or updates fund master + TER rows atomically (all or nothing).
func UpsertFunds(ctx context.Context, pool *pgxpool.Pool, rows []FundRow) error {
	return inTx(ctx, pool, func(tx pgx.Tx) error {
		for _, f := range rows {
			_, err := tx.Exec(ctx, `
				INSERT INTO mutual_funds (isin, amfi_code, name, amc, plan, category, equity_oriented,
				                          ter_bps, direct_ter_bps, exit_load_bps, exit_load_days, ter_as_of)
				VALUES ($1, NULLIF($2,0), $3, $4, $5::plan_type, $6, $7, $8, $9, $10, $11, $12)
				ON CONFLICT (isin) DO UPDATE SET
				  amfi_code = COALESCE(EXCLUDED.amfi_code, mutual_funds.amfi_code),
				  name = EXCLUDED.name, amc = EXCLUDED.amc, plan = EXCLUDED.plan, category = EXCLUDED.category,
				  equity_oriented = EXCLUDED.equity_oriented, ter_bps = EXCLUDED.ter_bps,
				  direct_ter_bps = EXCLUDED.direct_ter_bps, exit_load_bps = EXCLUDED.exit_load_bps,
				  exit_load_days = EXCLUDED.exit_load_days, ter_as_of = EXCLUDED.ter_as_of, updated_at = now()`,
				f.ISIN, f.AMFICode, f.Name, f.AMC, f.Plan, f.Category, f.EquityOriented,
				f.TERBps, f.DirectTERBps, f.ExitLoadBps, f.ExitLoadDays, f.TERAsOf)
			if err != nil {
				return fmt.Errorf("fund %s: %w", f.ISIN, err)
			}
		}
		return nil
	})
}

// ReplaceHoldings atomically replaces one fund's holdings for one month. It refuses when the book
// changed drastically since the previous disclosure unless force is set.
func ReplaceHoldings(ctx context.Context, pool *pgxpool.Pool, month time.Time, fh *FundHoldings, force bool) error {
	return inTx(ctx, pool, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `SELECT id FROM mutual_funds WHERE isin = $1`, fh.FundISIN).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrUnknownFund
			}
			return err
		}

		prev := map[string]int64{}
		rows, err := tx.Query(ctx, `
			SELECT stock_isin, weight_bps FROM fund_holdings
			WHERE fund_id = $1 AND as_of_month = (SELECT max(as_of_month) FROM fund_holdings WHERE fund_id = $1 AND as_of_month < $2)`,
			id, month)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s string
			var w int64
			if err := rows.Scan(&s, &w); err != nil {
				rows.Close()
				return err
			}
			prev[s] = w
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(prev) > 0 && !force && CarryOverBps(prev, fh.Rows) < MinCarryOverBps {
			return ErrBigChange
		}

		if _, err := tx.Exec(ctx, `DELETE FROM fund_holdings WHERE fund_id = $1 AND as_of_month = $2`, id, month); err != nil {
			return err
		}
		for _, r := range fh.Rows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO fund_holdings (fund_id, as_of_month, stock_isin, stock_name, weight_bps) VALUES ($1,$2,$3,$4,$5)`,
				id, month, r.StockISIN, r.StockName, r.WeightBps); err != nil {
				return err
			}
		}
		return nil
	})
}

type CardResult struct{ Created, Updated, Unchanged []string }

// normalise keeps only cap groups that rules actually reference, so equal rules compare equal.
func normalise(c cards.Card) cards.Card {
	caps := map[string]int64{}
	for _, r := range c.Rules {
		if r.CapGroup != "" {
			caps[r.CapGroup] = c.GroupCapPoints[r.CapGroup]
		}
	}
	c.GroupCapPoints = caps
	return c
}

type currentCard struct {
	id        int64
	version   int
	effective time.Time
	issuer    string
	card      cards.Card
}

func loadCurrent(ctx context.Context, tx pgx.Tx, slug string) (*currentCard, error) {
	cc := &currentCard{card: cards.Card{ID: slug, Rules: map[cards.Category]cards.CategoryRule{}, GroupCapPoints: map[string]int64{}}}
	err := tx.QueryRow(ctx, `
		SELECT id, version, effective_from, issuer, name, annual_fee_paise, fee_waiver_spend_paise, base_earn_bps, redeem_paise_per_point
		FROM credit_cards WHERE slug = $1 ORDER BY version DESC LIMIT 1`, slug).
		Scan(&cc.id, &cc.version, &cc.effective, &cc.issuer, &cc.card.Name, &cc.card.AnnualFeePaise,
			&cc.card.FeeWaiverSpendPaise, &cc.card.BaseEarnBps, &cc.card.RedeemPaisePerPoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT category::text, earn_bps, excluded, COALESCE(cap_group,''), COALESCE(monthly_cap_points,0)
		FROM card_category_rules WHERE card_id = $1`, cc.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cat, group string
		var r cards.CategoryRule
		var cp int64
		if err := rows.Scan(&cat, &r.EarnBps, &r.Excluded, &group, &cp); err != nil {
			return nil, err
		}
		r.CapGroup = group
		cc.card.Rules[cards.Category(cat)] = r
		if group != "" {
			cc.card.GroupCapPoints[group] = cp
		}
	}
	return cc, rows.Err()
}

func sameCard(a, b cards.Card) bool {
	return a.Name == b.Name && a.AnnualFeePaise == b.AnnualFeePaise && a.FeeWaiverSpendPaise == b.FeeWaiverSpendPaise &&
		a.BaseEarnBps == b.BaseEarnBps && a.RedeemPaisePerPoint == b.RedeemPaisePerPoint &&
		reflect.DeepEqual(a.Rules, b.Rules) && reflect.DeepEqual(a.GroupCapPoints, b.GroupCapPoints)
}

// ApplyCards versions card rules: unchanged cards are untouched (idempotent), changed cards close the
// current version at the new effectiveFrom and open version+1, so a future-dated devaluation takes
// effect on its own date without a deploy.
func ApplyCards(ctx context.Context, pool *pgxpool.Pool, in []CardInput) (CardResult, error) {
	var res CardResult
	err := inTx(ctx, pool, func(tx pgx.Tx) error {
		for _, ci := range in {
			card := normalise(ci.Card)
			from, _ := time.Parse("2006-01-02", ci.EffectiveFrom) // validated by ParseCardsJSON

			cur, err := loadCurrent(ctx, tx, ci.Slug)
			if err != nil {
				return fmt.Errorf("card %s: %w", ci.Slug, err)
			}
			version := 1
			switch {
			case cur == nil:
				res.Created = append(res.Created, ci.Slug)
			case sameCard(normalise(cur.card), card) && cur.issuer == ci.Issuer:
				res.Unchanged = append(res.Unchanged, ci.Slug)
				continue
			default:
				if !from.After(cur.effective) {
					return fmt.Errorf("card %s: effectiveFrom %s must be after the current version's %s",
						ci.Slug, ci.EffectiveFrom, cur.effective.Format("2006-01-02"))
				}
				if _, err := tx.Exec(ctx, `UPDATE credit_cards SET effective_to = $1 WHERE id = $2`, from, cur.id); err != nil {
					return err
				}
				version = cur.version + 1
				res.Updated = append(res.Updated, ci.Slug)
			}

			note := ci.ChangeNote
			if note == "" && version == 1 {
				note = "initial import"
			}
			var id int64
			err = tx.QueryRow(ctx, `
				INSERT INTO credit_cards (slug, issuer, name, annual_fee_paise, fee_waiver_spend_paise, base_earn_bps,
				                          redeem_paise_per_point, version, effective_from, change_note)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')) RETURNING id`,
				ci.Slug, ci.Issuer, card.Name, card.AnnualFeePaise, card.FeeWaiverSpendPaise, card.BaseEarnBps,
				card.RedeemPaisePerPoint, version, from, note).Scan(&id)
			if err != nil {
				return fmt.Errorf("card %s: %w", ci.Slug, err)
			}
			for cat, r := range card.Rules {
				var group *string
				var capPts *int64
				if r.CapGroup != "" {
					g, cp := r.CapGroup, card.GroupCapPoints[r.CapGroup]
					group, capPts = &g, &cp
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO card_category_rules (card_id, category, earn_bps, excluded, cap_group, monthly_cap_points)
					VALUES ($1, $2::spend_category, $3, $4, $5, $6)`,
					id, string(cat), r.EarnBps, r.Excluded, group, capPts); err != nil {
					return fmt.Errorf("card %s rule %s: %w", ci.Slug, cat, err)
				}
			}
		}
		return nil
	})
	return res, err
}

// EnrichFromAMFI attaches AMFI scheme codes to funds we already hold (AMFI has no TER, so it can never
// create funds). Returns (matched, unmatchedISINs).
func EnrichFromAMFI(ctx context.Context, pool *pgxpool.Pool, entries []AMFIEntry) (int, int, error) {
	matched := 0
	unmatched := 0
	err := inTx(ctx, pool, func(tx pgx.Tx) error {
		for _, e := range entries {
			if len(e.ISINs) == 0 {
				continue
			}
			tag, err := tx.Exec(ctx, `
				UPDATE mutual_funds SET amfi_code = $1
				WHERE id = (SELECT id FROM mutual_funds WHERE isin = ANY($2) AND amfi_code IS NULL ORDER BY id LIMIT 1)
				  AND NOT EXISTS (SELECT 1 FROM mutual_funds WHERE amfi_code = $1)`, e.Code, e.ISINs)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				matched++
			} else {
				unmatched++
			}
		}
		return nil
	})
	return matched, unmatched, err
}
