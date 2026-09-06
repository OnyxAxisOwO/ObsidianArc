-- Allowance windows now run from the moment an account registered, not from a
-- calendar every account shares.
--
-- window_start is part of a counter's primary key, and it used to mean "the
-- Monday of this week" or "the first of this month". It now means "the nth
-- period since this account signed up", so every row written before this
-- points at a bucket nothing will ever look up again.
--
-- They are cleared rather than left to the janitor. Leaving them would mean a
-- month of rows that no reader can reach and no writer can correct, and the
-- one thing that could reach them is a future period start landing on an old
-- boundary by coincidence — which would resurrect a stranger's count.
--
-- The cost is that everyone's allowance reads as full once, on the deploy
-- that applies this. The counters are an index over usage_records and were
-- never the source of truth, so nothing is lost that the ledger cannot still
-- answer.

DELETE FROM usage_counters;
