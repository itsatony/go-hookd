// Package internal - temporary file to declare dependencies
package internal

import (
	_ "github.com/itsatony/go-cuserr"
	_ "github.com/itsatony/go-pubbing"
	_ "github.com/itsatony/go-version"
	_ "github.com/lib/pq"
	_ "github.com/matoous/go-nanoid/v2"
	_ "github.com/stretchr/testify/assert"
	_ "github.com/stretchr/testify/require"
	_ "go.uber.org/zap"
	_ "go.uber.org/zap/zaptest"
)
