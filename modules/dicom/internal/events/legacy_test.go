package events

import (
	"context"
	"testing"
)

type capPub struct{ subjects, ids []string }

func (c *capPub) Publish(_ context.Context, subject, id string, _ any) error {
	c.subjects, c.ids = append(c.subjects, subject), append(c.ids, id)
	return nil
}
func (c *capPub) Check(context.Context) error { return nil }
func (c *capPub) Close() error                { return nil }

func TestLegacySubjects(t *testing.T) {
	c := &capPub{}
	p := legacyPub{c}
	if err := p.Publish(context.Background(), SubjectInstanceStored, "id1", nil); err != nil {
		t.Fatal(err)
	}
	if len(c.subjects) != 2 || c.subjects[0] != "libre.dcm.instance.stored" || c.subjects[1] != "rws.dicom.instance.stored" {
		t.Fatalf("subjects = %v", c.subjects)
	}
	if c.ids[0] == c.ids[1] {
		t.Fatal("legacy copy must have its own message id (JetStream de-duplicates per stream)")
	}
}
