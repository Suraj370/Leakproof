-- ILLUSTRATIVE DEMO DATA, not real scheme or card terms. Mirrors internal/catalog/seed.go (an integration
-- test asserts the two stay identical). Replace with real data via cmd/ingest.
INSERT INTO mutual_funds (isin, amfi_code, name, amc, plan, category, equity_oriented, ter_bps, direct_ter_bps, exit_load_bps, exit_load_days, ter_as_of) VALUES
 ('INFDEMO00001', 900001, 'Demo Bluechip Fund (Regular)',        'Demo AMC', 'REGULAR', 'Large Cap',   TRUE, 160, 70, 0, 0, DATE '2025-03-31'),
 ('INFDEMO00002', 900002, 'Demo Large Cap Index Fund (Direct)',  'Demo AMC', 'DIRECT',  'Index',       TRUE,  60, 60, 0, 0, DATE '2025-03-31'),
 ('INFDEMO00003', 900003, 'Demo Consumption Fund (Direct)',      'Demo AMC', 'DIRECT',  'Thematic',    TRUE,  90, 90, 0, 0, DATE '2025-03-31');

INSERT INTO fund_holdings (fund_id, as_of_month, stock_isin, stock_name, weight_bps)
SELECT f.id, DATE '2025-03-01', h.stock_isin, h.stock_name, h.weight_bps
FROM (VALUES
 ('INFDEMO00001', 'INE040A01034', 'HDFC Bank',           900),
 ('INFDEMO00001', 'INE090A01021', 'ICICI Bank',          800),
 ('INFDEMO00001', 'INE009A01021', 'Infosys',             700),
 ('INFDEMO00001', 'INE002A01018', 'Reliance Industries', 600),
 ('INFDEMO00001', 'INE467B01029', 'TCS',                 500),
 ('INFDEMO00002', 'INE040A01034', 'HDFC Bank',           850),
 ('INFDEMO00002', 'INE090A01021', 'ICICI Bank',          750),
 ('INFDEMO00002', 'INE009A01021', 'Infosys',             650),
 ('INFDEMO00002', 'INE002A01018', 'Reliance Industries', 550),
 ('INFDEMO00002', 'INE467B01029', 'TCS',                 450),
 ('INFDEMO00003', 'INE009A01021', 'Infosys',             200),
 ('INFDEMO00003', 'INE280A01028', 'Titan Company',       600),
 ('INFDEMO00003', 'INE021A01026', 'Asian Paints',        500)
) AS h(fund_isin, stock_isin, stock_name, weight_bps)
JOIN mutual_funds f ON f.isin = h.fund_isin;

INSERT INTO credit_cards (slug, issuer, name, annual_fee_paise, fee_waiver_spend_paise, base_earn_bps, redeem_paise_per_point, version, effective_from) VALUES
 ('demo-cashback',    'Demo Bank', 'Demo Cashback Card (5% online, capped)',            59900,  20000000, 100, 100, 1, DATE '2025-01-01'),
 ('demo-travel',      'Demo Bank', 'Demo Travel Card (points, Rs1/pt on flights)',    1000000,  80000000, 333, 100, 1, DATE '2025-01-01'),
 ('demo-flat',        'Demo Bank', 'Demo Flat 1.5% Card',                                   0,         0, 150, 100, 1, DATE '2025-01-01'),
 ('demo-points-low',  'Demo Bank', 'Demo Bank Points Card (Rs0.25/pt statement credit)', 50000,  10000000, 400,  25, 1, DATE '2025-01-01');

INSERT INTO card_category_rules (card_id, category, earn_bps, excluded, cap_group, monthly_cap_points)
SELECT c.id, r.category::spend_category, r.earn_bps, r.excluded, r.cap_group, r.cap
FROM (VALUES
 ('demo-cashback', 'ONLINE_SHOPPING', 500,  FALSE, 'online', 5000),
 ('demo-cashback', 'FUEL',              0,  TRUE,  NULL,     NULL),
 ('demo-travel',   'FLIGHTS_HOTELS', 1000,  FALSE, 'travel', 15000),
 ('demo-travel',   'FUEL',              0,  TRUE,  NULL,     NULL),
 ('demo-travel',   'UTILITIES',       100,  FALSE, NULL,     NULL),
 ('demo-flat',     'FUEL',              0,  TRUE,  NULL,     NULL)
) AS r(slug, category, earn_bps, excluded, cap_group, cap)
JOIN credit_cards c ON c.slug = r.slug;
