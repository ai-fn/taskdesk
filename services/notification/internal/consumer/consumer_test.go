package consumer

import (
	"strings"
	"testing"
	"time"
)

func TestPickTaskCompletedTmpl_KnownLang(t *testing.T) {
	data := map[string]string{"Title": "Buy groceries", "Date": "2026-05-29 10:00"}
	for _, lang := range []string{"en", "zh"} {
		tmpl := pickTaskCompletedTmpl(lang)
		var buf strings.Builder
		if err := tmpl.Subject.Execute(&buf, data); err != nil {
			t.Errorf("lang=%s subject execute: %v", lang, err)
		}
		if buf.Len() == 0 {
			t.Errorf("lang=%s subject is empty", lang)
		}
	}
}

func TestPickTaskCompletedTmpl_UnknownFallsBackToEn(t *testing.T) {
	en := pickTaskCompletedTmpl("en")
	unknown := pickTaskCompletedTmpl("fr")
	data := map[string]string{"Title": "T", "Date": "D"}

	var enBuf, unknownBuf strings.Builder
	_ = en.Subject.Execute(&enBuf, data)
	_ = unknown.Subject.Execute(&unknownBuf, data)

	if enBuf.String() != unknownBuf.String() {
		t.Errorf("unknown lang did not fall back to en: en=%q unknown=%q", enBuf.String(), unknownBuf.String())
	}
}

func TestBuildPlanEmail_ZhSubjectContainsZH(t *testing.T) {
	ev := DailyPlanEvent{
		Summary:     "今天的计划",
		GeneratedAt: time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
		Language:    "zh",
	}
	subject, body := buildPlanEmail(ev)
	if !strings.Contains(subject, "2026-05-29") {
		t.Errorf("ZH subject missing date: %q", subject)
	}
	if !strings.Contains(body, "今天的计划") {
		t.Errorf("ZH body missing summary: %q", body)
	}
}

func TestBuildPlanEmail_UnknownLangFallsBackToEn(t *testing.T) {
	ev := DailyPlanEvent{
		Summary:     "Today's plan",
		GeneratedAt: time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
		Language:    "xx", // unknown
	}
	evEn := ev
	evEn.Language = "en"

	subject, _ := buildPlanEmail(ev)
	subjectEn, _ := buildPlanEmail(evEn)

	if subject != subjectEn {
		t.Errorf("unknown lang did not fall back: xx=%q en=%q", subject, subjectEn)
	}
}
