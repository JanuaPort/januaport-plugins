package client

import (
	"strings"
	"testing"
)

// SEC Lauf 5 (zweite Schicht gegen spekulatives Laden): Die Ladeprüfung lehnt Seiten ab, die prerender,
// prefetch, preconnect oder dns-prefetch anfordern oder Speculation Rules tragen. Dynamisch per Skript deckt
// das nicht ab; dort halten die Schichten der Oberfläche (Ressourcenfilter, Netz aus, Features aus).
func TestInjectCSPLehntSpekulativesLadenAb(t *testing.T) {
	page := func(head, body string) []byte {
		return []byte("<!doctype html><html><head><title>x</title>" + head + "</head><body>" + body + "</body></html>")
	}
	for _, tc := range []struct {
		name string
		html []byte
		ok   bool
	}{
		{"stilblatt", page(`<link rel="stylesheet" href="a.css">`, ``), true},
		{"icon", page(`<link rel="icon" href="a.ico">`, ``), true},
		{"modulSkript", page(``, `<script type="module" src="a.js"></script>`), true},
		{"jsonSkript", page(``, `<script type="application/json">{}</script>`), true},
		{"prerender", page(`<link rel="prerender" href="x.html">`, ``), false},
		{"prefetch", page(`<link rel="prefetch" href="x.html">`, ``), false},
		{"preconnect", page(`<link rel="preconnect" href="https://example.org">`, ``), false},
		{"dnsPrefetch", page(`<link rel="dns-prefetch" href="https://example.org">`, ``), false},
		{"grossgeschrieben", page(`<link rel="PreFetch" href="x.html">`, ``), false},
		{"mitLeerzeichen", page(`<link rel="  prefetch  " href="x.html">`, ``), false},
		{"tokenliste", page(`<link rel="stylesheet prefetch" href="x.css">`, ``), false},
		{"imBody", page(``, `<link rel="prerender" href="x.html">`), false},
		{"imTemplate", page(``, `<template><link rel="prefetch" href="x.html"></template>`), false},
		{"speculationrules", page(``, `<script type="speculationrules">{"prerender":[{"urls":["x.html"]}]}</script>`), false},
		{"speculationrulesGross", page(``, `<script type="SpeculationRules">{}</script>`), false},
		{"speculationrulesLeer", page(`<script type=" speculationrules ">{}</script>`, ``), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := injectCSP(tc.html)
			if tc.ok && (err != nil || !strings.Contains(string(out), "Content-Security-Policy")) {
				t.Fatalf("abgelehnt: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("angenommen")
			}
		})
	}
}
