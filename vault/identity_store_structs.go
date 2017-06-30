package vault

import (
	"regexp"
	"time"

	memdb "github.com/hashicorp/go-memdb"
	"github.com/hashicorp/vault/helper/locksutil"
	"github.com/hashicorp/vault/logical"
	"github.com/hashicorp/vault/logical/framework"
	log "github.com/mgutz/logxi/v1"
)

const (
	// Storage prefixes
	entityPrefix = "entity/"

	// EntityAlias mount type
	entityAliasMountType = "EntityAlias"
)

var (
	// metaKeyFormatRegEx checks if a metadata key string is valid
	metaKeyFormatRegEx = regexp.MustCompile(`^[a-zA-Z0-9=/+_-]+$`).MatchString
)

const (
	// The meta key prefix reserved for Vault's internal use
	metaKeyReservedPrefix = "vault-"

	// The maximum number of metadata key pairs allowed to be registered
	metaMaxKeyPairs = 64

	// The maximum allowed length of a metadata key
	metaKeyMaxLength = 128

	// The maximum allowed length of a metadata value
	metaValueMaxLength = 512
)

// identityStore is composed of its own storage view and a MemDB which
// maintains active in-memory replicas of the storage contents indexed by
// multiple fields.
type identityStore struct {
	// identityStore is a secret backend in Vault
	*framework.Backend

	// view is the storage sub-view where all the artifacts of identity store
	// gets persisted
	view logical.Storage

	// db is the in-memory database where the storage artifacts gets replicated
	// to enable richer queries based on multiple indexes.
	db *memdb.MemDB

	// validateMountPathFunc is a utility from router which returns the
	// properties of the mount given the mount path.
	validateMountPathFunc func(string) *validateMountResponse

	// validateMountAccessorFunc is a utility from router which returnes the
	// properties of the mount given the mount accessor.
	validateMountAccessorFunc func(string) *validateMountResponse

	// entityLocks are a set of 256 locks to which all the entities will be
	// categorized to while performing storage modifications.
	entityLocks []*locksutil.LockEntry

	// logger is the server logger copied over from core
	logger log.Logger

	// StoragePacker is used to pack multiple storage entries into 256 buckets
	storagePacker *storagePacker
}

// entityStorageEntry represents an entity that gets persisted and indexed.
// Entity is fundamentally composed of zero or many personas.
type entityStorageEntry struct {
	// Personas are the identities that this entity is made of. This can be
	// empty as well to favor being able to create the entity first and then
	// incrementally adding personas.
	Personas []*personaIndexEntry `json:"personas" structs:"personas" mapstructure:"personas"`

	// ID is the unique identifier of the entity which always be a UUID. This
	// should never be allowed to be updated.
	ID string `json:"id" structs:"id" mapstructure:"id"`

	// Name is a unique identifier of the entity which is intended to be
	// human-friendly. The default name might not be human friendly since it
	// gets suffixed by a UUID, but it can optionally be updated, unlike the ID
	// field.
	Name string `json:"name" structs:"name" mapstructure:"name"`

	// Metadata represents the explicit metadata which is set by the
	// clients.  This is useful to tie any information pertaining to the
	// personas. This is a non-unique field of entity, meaning multiple
	// entities can have the same metadata set. Entities will be indexed based
	// on this explicit metadata. This enables virtual groupings of entities
	// based on its metadata.
	Metadata map[string]string `json:"metadata" structs:"metadata" mapstructure:"metadata"`

	// CreationTime is the time at which this entity is first created.
	CreationTime time.Time `json:"creation_time" structs:"creation_time" mapstructure:"creation_time"`

	// LastUpdateTime is the most recent time at which the properties of this
	// entity got modified. This is helpful in filtering out entities based on
	// its age and to take action on them, if desired.
	LastUpdateTime time.Time `json:"last_update_time" structs:"last_update_time" mapstructure:"last_update_time"`

	// MergedEntities are the entities which got merged to this one. Entities
	// will be indexed based on all the entities that got merged into it. This
	// helps to apply the actions on this entity on the tokens that are merged
	// to the merged entities. Merged entities will be deleted entirely and
	// this is the only trackable trail of its earlier presence.
	MergedEntities []string `json:"merged_entities" structs:"merged_entities" mapstructure:"merged_entities"`

	// Policies the entity is entitled to
	Policies []string `json:"policies" structs:"policies" mapstructure:"policies"`
}

// personaInput represents the information which is accepted over the API to
// register or modify any persona. This is different than the structure Vault
// populates internally for indexing.
type personaInput struct {
	Metadata  []string `json:"metadata" structs:"metadata" mapstructure:"metadata"`
	MountPath string   `json:"mount_path" structs:"mount_path" mapstructure:"mount_path"`
	Name      string   `json:"name" structs:"name" mapstructure:"name"`
}

// personaIndexEntry represents the persona that gets stored inside of the
// entity object in storage and also represents in an in-memory index of an
// persona object.
type personaIndexEntry struct {
	// ID is the unique identifier that represents this persona
	ID string `json:"id" structs:"id" mapstructure:"id"`

	// EntityID is the entity identifier to which this persona belongs to
	EntityID string `json:"entity_id" structs:"entity_id" mapstructure:"entity_id"`

	// MountID is the identifier of the mount entry to which this persona
	// belongs to. This is intended to be *always* kept internal to Vault.
	// Setting the structs tag to "-" to avoid accidentally returning it over
	// the API.
	MountID string `json:"mount_id" structs:"-" mapstructure:"mount_id"`

	// MountType is the backend mount's type to which this persona belongs to.
	// This enables categorically querying personas of specific backend
	// types.
	MountType string `json:"mount_type" structs:"mount_type" mapstructure:"mount_type"`

	// Metadata is the explicit metadata that clients set against an entity
	// which enables virtual grouping of personas. Personas will be indexed
	// against their metadata.
	Metadata map[string]string `json:"metadata" structs:"metadata" mapstructure:"metadata"`

	// Name is the identifier of this persona in its authentication source.
	// This does not uniquely identify a persona in Vault. This in conjunction
	// with MountID form to be the factors that represent a persona in a
	// unique way. Personas will be indexed based on this combined uniqueness
	// factor.
	Name string `json:"name" structs:"name" mapstructure:"name"`

	// CreationTime is the time at which this persona was first created
	CreationTime time.Time `json:"creation_time" structs:"creation_time" mapstructure:"creation_time"`

	// LastUpdateTime is the most recent time at which the properties of this
	// persona got modified. This is helpful in filtering out personas based
	// on its age and to take action on them, if desired.
	LastUpdateTime time.Time `json:"last_update_time" structs:"last_update_time" mapstructure:"last_update_time"`

	// MergedFrom is the identifier of the entity from which this persona is
	// transfered over to the entity to which it currently belongs to.
	MergedFrom string `json:"merged_from" structs:"merged_from" mapstructure:"merged_from"`
}
