package common

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGetAppHostingName(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want string
	}{
		{
			name: "generates name from UID",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
				},
			},
			want: "a24a730b8b134fd096ee900f99d87670",
		},
		{
			name: "returns existing label if set",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
					Labels: map[string]string{
						AppHostingNameLabel: "existinglabel123",
					},
				},
			},
			want: "existinglabel123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAppHostingName(1)
			if got != tt.want {
				t.Errorf("GetAppHostingName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetAppHostingNameLength(t *testing.T) {
	// UUID without hyphens is 32 chars, which fits in 40-char YANG constraint
	// pod := &v1.Pod{
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		UID: types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
	// 	},
	// }
	got := GetAppHostingName(1)
	if len(got) > 40 {
		t.Errorf("GetAppHostingName() length = %d, exceeds max 40", len(got))
	}
	if len(got) != 32 {
		t.Errorf("GetAppHostingName() length = %d, expected 32 (UUID without hyphens)", len(got))
	}
}

func TestGetAppHostingNameValidCharacters(t *testing.T) {
	// pod := &v1.Pod{
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		UID: types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
	// 	},
	// }
	result := GetAppHostingName(1)
	for _, c := range result {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("GetAppHostingName() = %q contains invalid character %q (expected hex only)", result, string(c))
		}
	}
}
