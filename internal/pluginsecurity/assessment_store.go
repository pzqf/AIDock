package pluginsecurity

import (
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) readAssessments(t workstore.Tree) (Authorities, map[string]Assessment, map[string]string, error) {
	keys := Authorities{Version: 1, Authorities: []Authority{}}
	values, docs := map[string]Assessment{}, map[string]string{}
	if e, ok := t.Entries["authorities.json"]; ok {
		b, err := m.Store.Data(e)
		if err != nil {
			return keys, values, docs, err
		}
		keys, err = ParseAuthorities(b)
		if err != nil {
			return keys, values, docs, err
		}
	}
	for p, e := range t.Entries {
		if !strings.HasPrefix(p, "assessments/") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(p, "assessments/"), ".json")
		if e.Dir || !hash(key) || !strings.HasSuffix(p, ".json") {
			return keys, values, docs, result.Fail("ASSESSMENT_CONTENT_INVALID", "签名评估缓存有未知内容", 5)
		}
		b, err := m.Store.Data(e)
		if err != nil {
			return keys, values, docs, err
		}
		a, err := ParseAssessment(b)
		if err != nil || a.Payload.Reference.SubjectSHA256 != key {
			return keys, values, docs, result.Fail("ASSESSMENT_CONTENT_INVALID", "签名评估缓存格式或路径绑定无效", 5)
		}
		values[key], docs[key] = a, string(b)
	}
	return keys, values, docs, nil
}
