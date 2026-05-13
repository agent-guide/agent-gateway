package configstore

import "errors"

// ErrNotFound is returned by store implementations when a requested record does not exist.
// Gateway layers should check for this error rather than depending on storage-specific errors
// (e.g. gorm.ErrRecordNotFound).
var ErrNotFound = errors.New("not found")
