package orchestration

import (
	"github.com/tetsup/flexraid/pkg/shared"
)

type runtimeObject struct {
	header           header
	children         map[string]*runtimeObject
	file             *runtimeFile
	linkTarget       string
	linkTargetLoaded bool
}

type runtimeFile struct {
	objectID    shared.ObjectID
	header      header
	openCount   uint64
	stripeCache map[uint64]*stripeCacheEntry
}

type stripeCacheEntry struct {
	data  []byte
	state stripeCacheState
}

type stripeCacheState uint8

const (
	stripeCacheClean stripeCacheState = iota
	stripeCacheDirty
)

type initializationWarning struct {
	objectID shared.ObjectID
	reason   string
}
