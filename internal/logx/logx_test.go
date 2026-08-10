package logx

import "testing"

func TestParseLevel(t *testing.T) {
	if ParseLevel("DEBUG") != LevelDebug {
		t.Fatal("debug")
	}
	if ParseLevel("warn") != LevelWarn {
		t.Fatal("warn")
	}
	if ParseLevel("") != LevelInfo {
		t.Fatal("default")
	}
}

func TestSubscribe(t *testing.T) {
	l := New(LevelInfo)
	ch := l.Subscribe(LevelInfo)
	defer l.Unsubscribe(ch)
	l.Info("hello", "k", 1)
	select {
	case e := <-ch:
		if e.Level != "info" || e.Message == "" {
			t.Fatalf("%+v", e)
		}
	default:
		t.Fatal("expected entry")
	}
}
