package main

import (
	"slices"
	"testing"
)

// The case file is only read by a run on the real model, so a typo would
// surface there, after spending tokens.
func TestCasesLoad(t *testing.T) {
	cases, err := loadCases("../../eval/cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	noAdvice := 0
	for _, c := range cases {
		if slices.Contains(c.Tags, "noadvice") {
			noAdvice++
			if !c.Expect.Unchanged {
				t.Errorf("%s: a question must not change anything", c.Name)
			}
		}
	}
	if noAdvice < 20 {
		t.Errorf("%d cases on advice, want at least 20", noAdvice)
	}
}
