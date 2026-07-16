package proaccountoperation_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

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
