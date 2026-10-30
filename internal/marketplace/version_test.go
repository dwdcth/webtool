package marketplace

import "testing"

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.9.0", "1.10.0", true}, // 数字比较而非字符串
		{"1.0.0", "1.0.0", false},
		{"2.0.0", "1.9.9", false},
		{"1.0", "1.0.1", true}, // 段数不同
		{"1.0.0", "2", true},
		// 注：预发布后缀（-beta）按字符串比较不遵循 semver 预发布语义，
		// unihub 市场条目均为 x.y.z，该局限可接受
	}
	for _, c := range cases {
		if got := VersionLess(c.a, c.b); got != c.want {
			t.Fatalf("VersionLess(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
