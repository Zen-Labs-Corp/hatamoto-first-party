package main

import (
	"errors"
	"testing"
)

func TestSettingsClampAndDefaults(t *testing.T) {
	f := newFake(t)
	s, err := loadSettings()
	if err != nil || s.MinRating != 4 || s.MaxItems != 6 {
		t.Fatalf("defaults %+v %v", s, err)
	}
	f.set("min_rating", 9)
	f.set("max_items", 500)
	s, _ = loadSettings()
	if s.MinRating != 5 || s.MaxItems != 48 {
		t.Fatalf("upper clamp %+v", s)
	}
	f.set("min_rating", -3)
	f.set("max_items", -1)
	s, _ = loadSettings()
	if s.MinRating != 1 || s.MaxItems != 1 {
		t.Fatalf("lower clamp %+v", s)
	}
}

func TestSourceNeedsKeyAndID(t *testing.T) {
	f := newFake(t)
	f.set("google_api_key", "k")
	f.set("trustpilot_business_unit_id", "u")
	s, _ := loadSettings()
	if s.googleOn() || s.tpOn() {
		t.Fatalf("half-configured sources must be off: %+v", s)
	}
	f.setBoth()
	s, _ = loadSettings()
	if !s.googleOn() || !s.tpOn() {
		t.Fatalf("both on expected: %+v", s)
	}
}

func TestSettingsHostErrorGivesDefaults(t *testing.T) {
	f := newFake(t)
	f.settingsErr = errors.New("down")
	s, err := loadSettings()
	if err == nil || s.googleOn() || s.MinRating != 4 {
		t.Fatalf("%+v %v", s, err)
	}
}
