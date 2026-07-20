package sqlite

import "database/sql"

func ensureProAccountTables(db *sql.DB) error {
	statements := []string{
		`create table if not exists pro_accounts (
			id text primary key,
			platform text not null,
			auth_type text not null,
			source_type text not null,
			plan_type text,
			name text,
			notes text,
			email text,
			enabled integer not null default 1,
			health_status text not null default 'unknown',
			last_error text,
			allowed_models_json text not null default '[]',
			model_mapping_json text not null default '{}',
			model_rule_version text,
			last_used_at_ms integer,
			last_tested_at_ms integer,
			expires_at_ms integer,
			deleted_at_ms integer,
			created_at_ms integer not null,
			updated_at_ms integer not null,
			version integer not null default 1
		)`,
		`create index if not exists idx_pro_accounts_platform on pro_accounts(platform)`,
		`create index if not exists idx_pro_accounts_auth_type on pro_accounts(auth_type)`,
		`create index if not exists idx_pro_accounts_enabled on pro_accounts(enabled)`,
		`create index if not exists idx_pro_accounts_health on pro_accounts(health_status)`,
		`create index if not exists idx_pro_accounts_updated on pro_accounts(updated_at_ms desc, id desc)`,
		`create index if not exists idx_pro_accounts_schedule_activity
			on pro_accounts(enabled desc, coalesce(last_used_at_ms, 0) desc, id desc)`,
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
			attribution_quality text not null default 'unknown',
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
			resolved_account_id text references pro_accounts(id) on delete set null,
			resolved_at_ms integer,
			first_seen_at_ms integer not null,
			last_seen_at_ms integer not null,
			created_at_ms integer not null,
			updated_at_ms integer not null
		)`,
		`create index if not exists idx_pro_binding_reviews_status on pro_account_binding_reviews(resolution_status, last_seen_at_ms desc)`,
		`create table if not exists pro_account_drafts (
			operation_id text primary key,
			idempotency_key text not null unique,
			operation_type text not null,
			pro_account_id text references pro_accounts(id) on delete set null,
			state text not null,
			version integer not null default 1,
			retry_count integer not null default 0,
			cleanup_deadline_ms integer not null,
			compensation_action text,
			error_code text,
			error_summary text,
			context_json text not null default '{}',
			created_at_ms integer not null,
			updated_at_ms integer not null
		)`,
		`create index if not exists idx_pro_account_drafts_recovery
			on pro_account_drafts(state, cleanup_deadline_ms, updated_at_ms)`,
		`create index if not exists idx_pro_account_drafts_account
			on pro_account_drafts(pro_account_id, updated_at_ms desc)`,
		`create unique index if not exists idx_pro_account_drafts_active_reauthorization
			on pro_account_drafts(pro_account_id)
			where operation_type = 'reauthorize'
				and pro_account_id is not null
				and state not in ('enabled', 'saved_disabled', 'cancelled', 'failed')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	if err := ensureProAccountColumns(db); err != nil {
		return err
	}
	if err := ensureProBindingColumns(db); err != nil {
		return err
	}
	return ensureProBindingReviewColumns(db)
}

func ensureProAccountColumns(db *sql.DB) error {
	rows, err := db.Query(`pragma table_info(pro_accounts)`)
	if err != nil {
		return err
	}
	hasVersion := false
	hasModelRuleVersion := false
	hasDeletedAt := false
	hasPlanType := false
	hasNotes := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "version" {
			hasVersion = true
		}
		if name == "model_rule_version" {
			hasModelRuleVersion = true
		}
		if name == "deleted_at_ms" {
			hasDeletedAt = true
		}
		if name == "plan_type" {
			hasPlanType = true
		}
		if name == "notes" {
			hasNotes = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasVersion {
		if _, err = db.Exec(`alter table pro_accounts add column version integer not null default 1`); err != nil {
			return err
		}
	}
	if !hasModelRuleVersion {
		if _, err = db.Exec(`alter table pro_accounts add column model_rule_version text`); err != nil {
			return err
		}
	}
	if !hasDeletedAt {
		if _, err = db.Exec(`alter table pro_accounts add column deleted_at_ms integer`); err != nil {
			return err
		}
	}
	if !hasPlanType {
		if _, err = db.Exec(`alter table pro_accounts add column plan_type text`); err != nil {
			return err
		}
	}
	if !hasNotes {
		_, err = db.Exec(`alter table pro_accounts add column notes text`)
	}
	return err
}

func ensureProBindingColumns(db *sql.DB) error {
	rows, err := db.Query(`pragma table_info(pro_account_bindings)`)
	if err != nil {
		return err
	}
	hasAttributionQuality := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "attribution_quality" {
			hasAttributionQuality = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if hasAttributionQuality {
		return nil
	}
	_, err = db.Exec(`alter table pro_account_bindings add column attribution_quality text not null default 'unknown'`)
	return err
}

func ensureProBindingReviewColumns(db *sql.DB) error {
	rows, err := db.Query(`pragma table_info(pro_account_binding_reviews)`)
	if err != nil {
		return err
	}
	hasResolvedAccountID := false
	hasResolvedAt := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		switch name {
		case "resolved_account_id":
			hasResolvedAccountID = true
		case "resolved_at_ms":
			hasResolvedAt = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasResolvedAccountID {
		if _, err := db.Exec(`alter table pro_account_binding_reviews add column resolved_account_id text references pro_accounts(id) on delete set null`); err != nil {
			return err
		}
	}
	if !hasResolvedAt {
		if _, err := db.Exec(`alter table pro_account_binding_reviews add column resolved_at_ms integer`); err != nil {
			return err
		}
	}
	return nil
}
