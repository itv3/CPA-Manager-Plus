package proaccountgateway

import "testing"

func TestProjectedLocatorAfterDelete(t *testing.T) {
	tests := []struct {
		name        string
		accounts    []AccountSnapshot
		oldAccount  AccountSnapshot
		replacement AccountSnapshot
		want        string
	}{
		{
			name:        "独立配置列表删除较早元素",
			oldAccount:  AccountSnapshot{SourceType: SourceCodexAPIKey, SourceLocator: "index:0"},
			replacement: AccountSnapshot{SourceType: SourceCodexAPIKey, SourceLocator: "index:2"},
			want:        "index:1",
		},
		{
			name: "单 Key Provider 删除后修正 Provider 下标",
			accounts: []AccountSnapshot{
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:0"},
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:1:key:0"},
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:2:key:0"},
			},
			oldAccount:  AccountSnapshot{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:0"},
			replacement: AccountSnapshot{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:2:key:0"},
			want:        "provider:1:key:0",
		},
		{
			name: "共享 Provider 删除单 Key 不移动后续 Provider",
			accounts: []AccountSnapshot{
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:0"},
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:1"},
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:2:key:0"},
			},
			oldAccount:  AccountSnapshot{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:0"},
			replacement: AccountSnapshot{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:2:key:0"},
			want:        "provider:2:key:0",
		},
		{
			name: "同 Provider 删除较早 Key",
			accounts: []AccountSnapshot{
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:0"},
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:1"},
				{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:2"},
			},
			oldAccount:  AccountSnapshot{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:0"},
			replacement: AccountSnapshot{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:2"},
			want:        "provider:0:key:1",
		},
		{
			name:        "不同来源互不影响",
			oldAccount:  AccountSnapshot{SourceType: SourceCodexAPIKey, SourceLocator: "index:0"},
			replacement: AccountSnapshot{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:3:key:0"},
			want:        "provider:3:key:0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ProjectedLocatorAfterDelete(test.accounts, test.oldAccount, test.replacement)
			if err != nil {
				t.Fatalf("计算删除后定位：%v", err)
			}
			if got != test.want {
				t.Fatalf("删除后定位 = %q，期望 %q", got, test.want)
			}
		})
	}
}

func TestSharesEnabledStateOnlyForMultipleKeysInSameProvider(t *testing.T) {
	accounts := []AccountSnapshot{
		{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:0"},
		{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:0:key:1"},
		{SourceType: SourceOpenAICompatibility, SourceLocator: "provider:1:key:0"},
		{SourceType: SourceCodexAPIKey, SourceLocator: "index:0"},
	}
	if !SharesEnabledState(accounts, accounts[0]) {
		t.Fatal("同一 Provider 的多个 Key 应共享启停状态")
	}
	if SharesEnabledState(accounts, accounts[2]) {
		t.Fatal("单 Key Provider 不应被判定为共享启停状态")
	}
	if SharesEnabledState(accounts, accounts[3]) {
		t.Fatal("独立配置账号不应被判定为共享启停状态")
	}
}
