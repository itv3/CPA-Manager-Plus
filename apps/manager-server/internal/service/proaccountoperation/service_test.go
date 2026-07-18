package proaccountoperation_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/proaccountdraft"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
)

func TestServiceRejectsNestedConcreteSensitiveMaps(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	service := proaccountoperation.New(proaccountdraft.New(db))
	_, _, err = service.Start(context.Background(), proaccountoperation.StartInput{
		OperationID: "operation-1", IdempotencyKey: "key-1", OperationType: "add",
		Context: map[string]any{"nested": map[string]string{"api_key": "不得持久化"}},
	})
	if !errors.Is(err, proaccountoperation.ErrSensitiveOperationData) {
		t.Fatalf("敏感上下文错误 = %v", err)
	}
}

func TestSavedDisabledIsSerializedAndCannotTransition(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	service := proaccountoperation.New(proaccountdraft.New(db))
	operation, created, err := service.Start(context.Background(), proaccountoperation.StartInput{
		OperationID: "operation-saved-disabled", IdempotencyKey: "key-saved-disabled", OperationType: "add",
	})
	if err != nil || !created {
		t.Fatalf("创建操作：operation=%#v created=%v err=%v", operation, created, err)
	}
	operation, err = service.Transition(context.Background(), operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateSavedDisabled,
	})
	if err != nil || operation.State != model.ProOperationStateSavedDisabled {
		t.Fatalf("进入停用终态：operation=%#v err=%v", operation, err)
	}
	raw, err := json.Marshal(operation)
	if err != nil || !strings.Contains(string(raw), `"state":"saved_disabled"`) {
		t.Fatalf("终态序列化：json=%s err=%v", raw, err)
	}
	_, err = service.Transition(context.Background(), operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateFailed,
	})
	if !errors.Is(err, proaccountoperation.ErrInvalidStateTransition) {
		t.Fatalf("停用终态仍可转换，错误 = %v", err)
	}
}

func TestServiceFindsPersistedActiveReauthorization(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatalf("打开 SQLite：%v", err)
	}
	defer db.Close()
	if _, err = db.Exec(`insert into pro_accounts (
		id, platform, auth_type, source_type, enabled, health_status, created_at_ms, updated_at_ms
	) values ('account-1', 'openai', 'oauth', 'auth_file', 1, 'unknown', 1, 1)`); err != nil {
		t.Fatalf("创建测试账号：%v", err)
	}
	service := proaccountoperation.New(proaccountdraft.New(db))
	operation, created, err := service.Start(context.Background(), proaccountoperation.StartInput{
		OperationID: "reauthorize-1", IdempotencyKey: "reauthorize-key-1", OperationType: "reauthorize", ProAccountID: "account-1",
	})
	if err != nil || !created {
		t.Fatalf("创建重新授权：operation=%#v created=%v err=%v", operation, created, err)
	}
	active, found, err := service.FindActiveReauthorization(context.Background(), "account-1")
	if err != nil || !found || active.OperationID != operation.OperationID {
		t.Fatalf("查询活动重新授权：operation=%#v found=%v err=%v", active, found, err)
	}
	operation, err = service.Transition(context.Background(), operation.OperationID, proaccountoperation.TransitionInput{
		ExpectedVersion: operation.Version, State: model.ProOperationStateCancelled,
	})
	if err != nil {
		t.Fatalf("取消重新授权：%v", err)
	}
	if _, found, err = service.FindActiveReauthorization(context.Background(), "account-1"); err != nil || found {
		t.Fatalf("取消后仍返回活动会话：found=%v err=%v", found, err)
	}
}
