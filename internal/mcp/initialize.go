package mcp

import (
	"bytes"
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/codec"
)

// Handshake metadata needs less space than invitation-bearing tool requests.
const maxInitialize = 16 * 1024

// Only handshake information is extensible. codec still validates duplicate keys,
// exact known-field types and bounds; no metadata is used for binding or commands.
func informationalFields(raw json.RawMessage, fields map[string]any) bool {
	var object map[string]json.RawMessage
	if codec.Decode(raw, maxInitialize, &object) != nil || object == nil {
		return false
	}
	for name, out := range fields {
		if value, ok := object[name]; ok {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || codec.Decode(value, maxInitialize, out) != nil {
				return false
			}
		}
	}
	return true
}

func validImplementation(raw json.RawMessage) bool {
	var name, version, title, description, website string
	var icons []json.RawMessage
	if !informationalFields(raw, map[string]any{"name": &name, "version": &version, "title": &title, "description": &description, "websiteUrl": &website, "icons": &icons}) {
		return false
	}
	if name == "" || version == "" || len(name) > 256 || len(version) > 256 || len(title) > 256 || len(description) > 4096 || len(website) > 2048 || len(icons) > 16 {
		return false
	}
	for _, rawIcon := range icons {
		var src, mime, theme string
		var sizes []string
		if !informationalFields(rawIcon, map[string]any{"src": &src, "mimeType": &mime, "sizes": &sizes, "theme": &theme}) || src == "" || len(src) > 8192 || len(mime) > 256 || len(sizes) > 16 || (theme != "" && theme != "light" && theme != "dark") {
			return false
		}
		for _, size := range sizes {
			if len(size) > 64 {
				return false
			}
		}
	}
	return true
}

func validClientCapabilities(capabilities map[string]json.RawMessage) bool {
	for _, name := range []string{"experimental", "roots", "sampling", "elicitation", "tasks"} {
		raw, ok := capabilities[name]
		if !ok {
			continue
		}
		var listChanged bool
		var context, tools, form, url, list, cancel, requests map[string]json.RawMessage
		fields := map[string]any{}
		switch name {
		case "roots":
			fields["listChanged"] = &listChanged
		case "sampling":
			fields["context"], fields["tools"] = &context, &tools
		case "elicitation":
			fields["form"], fields["url"] = &form, &url
		case "tasks":
			fields["list"], fields["cancel"], fields["requests"] = &list, &cancel, &requests
		}
		if !informationalFields(raw, fields) {
			return false
		}
	}
	return true
}
