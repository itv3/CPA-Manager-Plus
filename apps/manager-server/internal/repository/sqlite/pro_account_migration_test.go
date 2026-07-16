package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestProAccountMigrationUpgradesLegacyTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", dataSourceName(path))
	if err != nil {
		t.Fatalf("打开旧数据库：%v", err)
	}
	_, err = db.Exec(`create table pro_accounts (
		id text primary key, platform text not null, auth_type text not null, source_type text not null,
		name text, email text, enabled integer not null default 1, health_status text not null default 'unknown',
		last_error text, allowed_models_json text not null default '[]', model_mapping_json text not null default '{}',
		last_used_at_ms integer, last_tested_at_ms integer, expires_at_ms integer,
		created_at_ms integer not null, updated_at_ms integer not null
	)`)
	if err != nil {
		t.Fatalf("创建旧账号表：%v", err)
	}
	if _, err := db.Exec(`insert into pro_accounts (
		id, platform, auth_type, source_type, created_at_ms, updated_at_ms
	) values ('legacy-id', 'openai', 'oauth', 'auth_file', 1, 1)`); err != nil {
		t.Fatalf("写入旧账号：%v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("执行迁移：%v", err)
	}
	defer db.Close()
	var version int64
	var ruleVersion sql.NullString
	if err := db.QueryRow(`select version, model_rule_version from pro_accounts where id = 'legacy-id'`).Scan(&version, &ruleVersion); err != nil {
		t.Fatalf("读取迁移结果：%v", err)
	}
	if version != 1 || ruleVersion.Valid {
		t.Fatalf("迁移字段 = version:%d rule:%#v", version, ruleVersion)
	}
	var draftTable string
	if err := db.QueryRow(`select name from sqlite_master where type = 'table' and name = 'pro_account_drafts'`).Scan(&draftTable); err != nil {
		t.Fatalf("草稿表未创建：%v", err)
	}
}
