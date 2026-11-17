-- NAV on 31-Jan-2018, used to step up the cost of equity units bought before 1-Feb-2018 (Sec. 55(2)(ac)).
-- NULL = unknown: the engine falls back to actual cost and tells the user tax may be overstated.
ALTER TABLE mutual_funds
  ADD COLUMN nav_2018_01_31 NUMERIC(14,4) CHECK (nav_2018_01_31 IS NULL OR nav_2018_01_31 > 0);
