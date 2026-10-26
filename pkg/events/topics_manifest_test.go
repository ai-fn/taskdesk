package events

import (
	"os"
	"strings"
	"testing"
)

// Strimzi names a topic after metadata.name unless spec.topicName is set, and k8s names cannot
// contain dots, so every topic must set it explicitly.
func TestKafkaTopicManifestDeclaresEveryTopic(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/k8s/base/kafka/topics.yaml")
	if err != nil {
		t.Fatal(err)
	}

	declared := map[string]bool{}
	for i, doc := range strings.Split(string(raw), "\n---") {
		if !strings.Contains(doc, "kind: KafkaTopic") {
			continue
		}
		var name string
		for _, line := range strings.Split(doc, "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "topicName:"); ok {
				name, _, _ = strings.Cut(v, " #") // inline comment
				name = strings.TrimSpace(name)
			}
		}
		if name == "" {
			t.Errorf("KafkaTopic #%d has no spec.topicName", i+1)
		}
		declared[name] = true
	}

	// The daily-plan topic is a private const in ai and notification, so it is spelled out here.
	for _, topic := range []string{Topic, AuthEmailTopic, FileTopic, "daily-plan.generated.v1"} {
		for _, name := range []string{topic, topic + ".dlq"} {
			if !declared[name] {
				t.Errorf("topic %q is not declared in topics.yaml", name)
			}
		}
	}
}
