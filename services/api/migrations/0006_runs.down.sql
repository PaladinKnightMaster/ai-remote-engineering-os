-- Reverse 0006_runs. Drop run_steps first (FK → runs), then runs.
DROP TABLE IF EXISTS run_steps;
DROP TABLE IF EXISTS runs;
