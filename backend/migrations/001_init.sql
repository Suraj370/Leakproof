CREATE TYPE plan_type AS ENUM ('DIRECT','REGULAR');
CREATE TYPE spend_category AS ENUM ('DINING','GROCERIES','FLIGHTS_HOTELS','UTILITIES','FUEL',
                                    'ONLINE_SHOPPING','OFFLINE_POS','EXCLUDED');

CREATE TABLE mutual_funds (
  id               BIGSERIAL PRIMARY KEY,
  isin             CHAR(12) NOT NULL UNIQUE,
  amfi_code        INT UNIQUE,
  name             TEXT NOT NULL,
  amc              TEXT NOT NULL,
  plan             plan_type NOT NULL,
  category         TEXT NOT NULL,
  equity_oriented  BOOLEAN NOT NULL,
  ter_bps          INT NOT NULL CHECK (ter_bps BETWEEN 0 AND 500),
  direct_ter_bps   INT CHECK (direct_ter_bps BETWEEN 0 AND 500),
  exit_load_bps    INT NOT NULL DEFAULT 0 CHECK (exit_load_bps BETWEEN 0 AND 500),
  exit_load_days   INT NOT NULL DEFAULT 0,
  ter_as_of        DATE NOT NULL,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE fund_holdings (
  fund_id      BIGINT NOT NULL REFERENCES mutual_funds(id) ON DELETE CASCADE,
  as_of_month  DATE   NOT NULL,
  stock_isin   CHAR(12) NOT NULL,
  stock_name   TEXT NOT NULL,
  sector       TEXT,
  weight_bps   INT NOT NULL CHECK (weight_bps BETWEEN 0 AND 10000),
  PRIMARY KEY (fund_id, as_of_month, stock_isin)
);
CREATE INDEX fund_holdings_stock_idx ON fund_holdings (stock_isin, as_of_month);

CREATE TABLE credit_cards (
  id                       BIGSERIAL PRIMARY KEY,
  slug                     TEXT NOT NULL,
  issuer                   TEXT NOT NULL,
  name                     TEXT NOT NULL,
  annual_fee_paise         BIGINT NOT NULL CHECK (annual_fee_paise >= 0),
  fee_waiver_spend_paise   BIGINT NOT NULL DEFAULT 0,
  base_earn_bps            INT NOT NULL,
  redeem_paise_per_point   INT NOT NULL CHECK (redeem_paise_per_point > 0),
  version                  INT NOT NULL DEFAULT 1,
  effective_from           DATE NOT NULL,
  effective_to             DATE,
  change_note              TEXT,
  UNIQUE (slug, version)
);

CREATE TABLE card_category_rules (
  card_id             BIGINT NOT NULL REFERENCES credit_cards(id) ON DELETE CASCADE,
  category            spend_category NOT NULL,
  earn_bps            INT NOT NULL DEFAULT 0,
  excluded            BOOLEAN NOT NULL DEFAULT FALSE,
  cap_group           TEXT,
  monthly_cap_points  BIGINT CHECK (monthly_cap_points IS NULL OR monthly_cap_points > 0),
  PRIMARY KEY (card_id, category),
  CHECK ((cap_group IS NULL) = (monthly_cap_points IS NULL))
);

-- Guest mode never writes here unless the user explicitly taps "Save my score".
-- No PII columns by design: aggregates only.
CREATE TABLE user_reports (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at            TIMESTAMPTZ NOT NULL DEFAULT now() + interval '90 days',
  reward_yield_bps      INT NOT NULL,
  overlap_bps           INT NOT NULL,
  annual_leak_paise     BIGINT NOT NULL,
  card_leak_paise       BIGINT NOT NULL,
  mf_leak_paise         BIGINT NOT NULL,
  mf_ruleset_month      DATE NOT NULL,
  card_ruleset_version  TEXT NOT NULL,
  share_slug            TEXT UNIQUE
);
CREATE INDEX user_reports_expiry_idx ON user_reports (expires_at);
