package sqlite

import "database/sql"

func ensureProAccountScheduledTestTables(db *sql.DB) error {
	statements := []string{
		`create table if not exists pro_account_scheduled_tests (
			id integer primary key autoincrement,
			account_id text not null references pro_accounts(id) on delete cascade,
			model text not null default '',
			cron_expression text not null default '*/30 * * * *',
			enabled integer not null default 1,
			max_results integer not null default 50,
			auto_recover integer not null default 0,
			last_run_at_ms integer,
			next_run_at_ms integer,
			created_at_ms integer not null,
			updated_at_ms integer not null
		)`,
		`create index if not exists idx_pro_scheduled_tests_account
			on pro_account_scheduled_tests(account_id, created_at_ms desc)`,
		`create index if not exists idx_pro_scheduled_tests_due
			on pro_account_scheduled_tests(next_run_at_ms, id) where enabled = 1`,
		`create table if not exists pro_account_scheduled_test_results (
			id integer primary key autoincrement,
			plan_id integer not null references pro_account_scheduled_tests(id) on delete cascade,
			status text not null default 'success',
			status_code integer,
			response_text text not null default '',
			error_code text not null default '',
			error_message text not null default '',
			retryable integer not null default 0,
			latency_ms integer not null default 0,
			started_at_ms integer not null,
			finished_at_ms integer not null,
			created_at_ms integer not null
		)`,
		`create index if not exists idx_pro_scheduled_test_results_plan
			on pro_account_scheduled_test_results(plan_id, created_at_ms desc, id desc)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
