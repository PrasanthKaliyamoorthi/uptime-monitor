package web

import "embed"

//go:embed static/* templates/* templates/admin/* templates/partials/*
var Files embed.FS
