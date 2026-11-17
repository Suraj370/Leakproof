-- Annual-spend milestone bonuses (e.g. "Rs8L spend in a year => Rs10,000 voucher"). One row per threshold.
CREATE TABLE card_milestones (
  card_id            BIGINT NOT NULL REFERENCES credit_cards(id) ON DELETE CASCADE,
  annual_spend_paise BIGINT NOT NULL CHECK (annual_spend_paise > 0),
  bonus_value_paise  BIGINT NOT NULL CHECK (bonus_value_paise > 0),
  PRIMARY KEY (card_id, annual_spend_paise)
);

-- Demo data: mirrors internal/catalog/seed.go (an integration test keeps them identical).
INSERT INTO card_milestones (card_id, annual_spend_paise, bonus_value_paise)
SELECT id, 80000000, 1000000 FROM credit_cards WHERE slug = 'demo-travel';
