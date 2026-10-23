package upstream

import "testing"

func TestColdStartSeedsUseCapturedNormalInferenceIDs(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"GLM-5.3", "glm-5.3"},
		{"DeepSeek-V4.1-Flash", "deepseek-v4.1-flash"},
		{"glm-5.2", "GLM-5.2"},
		{"qwen3-vl-235b", "Qwen3-VL-235B"},
	} {
		resolved, benefit := ResolveModelForAuth(t.Name()+"-cold", tc.input, nil)
		if resolved != tc.want || benefit {
			t.Errorf("cold input=%q wire=%q benefit=%v want=%q normal route", tc.input, resolved, benefit, tc.want)
		}
	}
	for _, input := range []string{"GLM-5.4", "DeepSeek-V4.1-Pro", "Unknown-Exact-ID"} {
		if resolved, _ := ResolveModelForAuth(t.Name()+"-cold", input, nil); resolved != input {
			t.Errorf("unobserved model was guessed: input=%q resolved=%q", input, resolved)
		}
	}
}
