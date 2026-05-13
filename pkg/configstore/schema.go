package configstore

// ConfigObjectCodec owns serialization and deserialization for one object family.
type ConfigObjectCodec interface {
	Encode(obj any) ([]byte, error)
	Decode(data []byte) (any, error)
}

// ObjectUnwrapper exposes the underlying persisted object when an adapter needs
// to attach extra metadata without changing the stored payload shape.
type ObjectUnwrapper interface {
	ConfigStoreObject() any
}

// TagCarrier carries a tag value outside the persisted object payload.
type TagCarrier interface {
	ConfigStoreTag() string
}

// ObjectMetadata extracts storage metadata from one persisted object family.
type ObjectMetadata interface {
	PrimaryKey(obj any) ([]any, error)
	Tag(obj any) (string, bool, error)
	Indexes(obj any) (map[string]any, error)
}

// MetadataFuncs adapts functions to ObjectMetadata.
type MetadataFuncs struct {
	PrimaryKeyFunc func(obj any) ([]any, error)
	TagFunc        func(obj any) (string, bool, error)
	IndexesFunc    func(obj any) (map[string]any, error)
}

func (m MetadataFuncs) PrimaryKey(obj any) ([]any, error) {
	if m.PrimaryKeyFunc == nil {
		return nil, nil
	}
	return m.PrimaryKeyFunc(obj)
}

func (m MetadataFuncs) Tag(obj any) (string, bool, error) {
	if m.TagFunc == nil {
		return "", false, nil
	}
	return m.TagFunc(obj)
}

func (m MetadataFuncs) Indexes(obj any) (map[string]any, error) {
	if m.IndexesFunc == nil {
		return nil, nil
	}
	return m.IndexesFunc(obj)
}

type IndexSchema struct {
	Name   string
	Column string
	Unique bool
}

// StoreSchema describes how one object family is persisted.
type StoreSchema struct {
	Name  string
	Kind  string
	Table string

	PrimaryKeyColumns []string
	TagColumn         string
	DataColumn        string

	IndexColumns []IndexSchema
	Timestamped  bool

	Codec    ConfigObjectCodec
	Metadata ObjectMetadata
}
