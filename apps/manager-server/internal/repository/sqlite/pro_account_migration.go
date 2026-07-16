package sqlite

import "database/sql"

func ensureProAccountTables(db *sql.DB) error {
	statements := []string{
		`create table if not exists pro_accounts (
			id text primary key,
			platform text not null,
			auth_type text not null,
			source_type text not null,
			name text,
			email text,
			enabled integer not null default 1,
			health_status text not null default 'unknown',
			last_error text,
			allowed_models_json text not null default '[]',
			model_mapping_json text not null default '{}',
			last_used_at_ms integer,
			last_tested_at_ms integer,
			expires_at_ms integer,
			created_at_ms integer not null,
			updated_at_ms integer not null
		)`,
		`create index if not exists idx_pro_accounts_platform on pro_accounts(platform)`,
		`create index if not exists idx_pro_accounts_auth_type on pro_accounts(auth_type)`,
		`create index if not exists idx_pro_accounts_enabled on pro_accounts(enabled)`,
		`create index if not exists idx_pro_accounts_health on pro_accounts(health_status)`,
		`create index if not exists idx_pro_accounts_updated on pro_accounts(updated_at_ms desc, id desc)`,
		`create table if not exists pro_account_bindings (
			id integer primary key autoincrement,
			pro_account_id text not null references pro_accounts(id) on delete cascade,
			auth_index text,
			source_type text not null,
			source_locator text not null,
			source_fingerprint text,
			binding_status text not null,
			is_current integer not null default 1,
			valid_from_ms integer not null,
			valid_to_ms integer,
			first_seen_at_ms integer not null,
			last_seen_at_ms integer not null,
			created_at_ms integer not null,
			updated_at_ms integer not null
		)`,
		`create unique index if not exists idx_pro_bindings_current_discovery
			on pro_account_bindings(source_type, source_locator, coalesce(auth_index, '')) where is_current = 1`,
		`create index if not exists idx_pro_bindings_account on pro_account_bindings(pro_account_id, is_current)`,
		`create index if not exists idx_pro_bindings_fingerprint on pro_account_bindings(source_fingerprint, is_current)`,
		`create index if not exists idx_pro_bindings_validity on pro_account_bindings(auth_index, valid_from_ms, valid_to_ms)`,
		`create table if not exists pro_account_binding_reviews (
			id integer primary key autoincrement,
			discovery_key text not null unique,
			source_type text not null,
			source_locator text not null,
			auth_index text,
			source_fingerprint text,
			resolution_status text not null,
			candidate_ids_json text not null default '[]',
			reason_code text not null,
			first_seen_at_ms integer not null,
			last_seen_at_ms integer not null,
			created_at_ms integer not null,
			updated_at_ms integer not null
		)`,
		`create index if not exists idx_pro_binding_reviews_status on pro_account_binding_reviews(resolution_status, last_seen_at_ms desc)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
