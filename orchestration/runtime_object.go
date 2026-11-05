package orchestration

import "github.com/tetsup/flexraid/pkg/shared"

type runtimeObject struct {
	header          header
	children        map[string]*runtimeObject
	file            *runtimeFile
	linkTarget      string
	linkTargetLoaded bool
}

type runtimeFile struct {
	objectID shared.ObjectID
	header   header
}

type initializationWarning struct {
	objectID shared.ObjectID
	reason   string
}
