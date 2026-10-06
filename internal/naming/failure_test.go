package naming

import (
	"testing"

	"video-rename/internal/domain"
)

func TestFailureTitleSubstringRules(t *testing.T) {
	for _, test := range []struct {
		name     string
		title    string
		keywords []string
		failed   bool
	}{
		{"default marker", "命名失败：画面不足", nil, true},
		{"default refusal", "模型拒绝回答该标题", nil, true},
		{"default unfinished", "很抱歉，无法完成此次任务", nil, true},
		{"default mixed case", "SoRrY, I cannot help", nil, true},
		{"ordinary title", "雨后街道上孩子们奔跑的午后", nil, false},
		{"Chinese sentence", "本次因看不清画面而停止命名", []string{"看不清画面"}, true},
		{"English sentence", "I CANNOT identify this scene", []string{"I cannot identify"}, true},
		{"marker removed", "命名失败", []string{"拒绝"}, false},
		{"explicitly disabled", "命名失败，拒绝，无法完成，SORRY", []string{}, false},
		{"match before cleaning", "a/b拒绝:c", []string{"a/b拒绝:c"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := FailureTitleError(test.title, test.keywords)
			if (err != nil) != test.failed {
				t.Fatalf("title=%q rules=%v err=%v", test.title, test.keywords, err)
			}
			if err != nil && (err.Code != domain.CodeNamingFailed || err.Global) {
				t.Fatalf("wrong failure classification: %+v", err)
			}
		})
	}
}
