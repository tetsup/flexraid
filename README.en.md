Flexible RAID

Overview

Flexible RAID is a userspace distributed filesystem designed for file storage, particularly media-server workloads.

It presents a single hierarchical filesystem through FUSE while distributing data across ordinary filesystems on multiple drives. It does not directly manage raw disks or require a host-managed RAID volume.

The system supports configurable data placement and redundancy policies. Small files can be mirrored, while larger files can transition to striped storage with parity. Placement parameters should be tunable and extensible to support different file types and workloads.

Architecture

The system consists of four layers:

1. I/O
2. Distribution
3. Orchestration
4. FUSE

┌─────────────────────────────┐
│ FUSE │
│ Filesystem interface │
└──────────────┬──────────────┘
│
┌──────────────▼──────────────┐
│ Orchestration │
│ Policy selection, routing, │
│ capacity and fault handling │
└──────────────┬──────────────┘
│
┌──────────────▼──────────────┐
│ Distribution │
│ Mirroring, striping, parity,│
│ recovery and migration │
└──────────────┬──────────────┘
│
┌──────────────▼──────────────┐
│ I/O │
│ Physical file operations │
└─────────────────────────────┘

1. I/O Layer

Responsibilities

- Read and write physical files.
- Support offset-based and partial reads and writes.
- Create, resize, synchronize and delete physical files as required.
- Report physical I/O failures to the calling layer.
- Provide the low-level operations required by the Distribution layer.

Constraints

- Operates on ordinary files managed by the underlying operating system filesystem.
- Does not access raw disks or implement RAID directly.
- Does not decide data placement, redundancy policies or recovery strategies.

The Distribution layer determines how logical data maps to physical files and invokes I/O operations accordingly.

2. Distribution Layer

Responsibilities

- Implement data placement and redundancy schemes.
- Support mirroring and striping with parity.
- Map logical file offsets to physical storage locations.
- Generate and reconstruct redundant data.
- Recover readable data when individual physical operations fail, where the configured redundancy permits it.
- Migrate data between placement schemes.
- Maintain the metadata required to interpret each file's physical layout.

Placement schemes

The initial implementation should support:

- Mirroring: Store redundant copies of a file across multiple drives.
- Striping with parity: Divide data into chunks, distribute them across data drives, and store parity information for recovery.

The design must not assume that all available drives participate in every file's layout. The number of data drives, parity drives and mirror copies should be configurable within the constraints of the selected scheme and available storage.

Configurable placement

Placement parameters should be configurable independently, including:

- Minimum file size for migration from mirroring to striping.
- Stripe chunk size.
- Number of participating data drives.
- Number of parity drives.
- Number of mirrored copies.
- Other scheme-specific parameters introduced in the future.

These parameters are tuning guidelines, not fixed architectural assumptions. Changing configuration must not make existing files unreadable.

Each file's actual placement must remain identifiable independently of the current default configuration. Existing files continue to use their recorded layout until an explicit migration changes it.

Mirroring-to-striping migration

Small files should initially use mirroring. When a file exceeds a configurable threshold, it may transition to striped storage.

The Distribution layer owns the migration procedure because it already understands both source and destination layouts.

The migration should follow these principles:

1. Keep the original mirrored data available while constructing the striped representation.
2. Read and transform data in bounded-size buffers rather than requiring the entire file to reside in memory.
3. Write the destination data and redundancy information.
4. Synchronize and validate the destination sufficiently to establish that it is usable.
5. Commit the new layout metadata only after the destination is ready.
6. Reclaim the old representation only after the new layout is safely committed.

The migration must have a recoverable state model so that interruption or a physical I/O failure does not leave the file interpreted as a completed striped file when its destination is incomplete.

The exact commit protocol and recovery procedure remain to be specified.

Stripe sizing

The stripe chunk size should be configurable and aligned to a suitable multiple of the underlying filesystem's block size.

A possible initial sizing heuristic is to relate the chunk size to the migration threshold and the number of data drives. This is only a starting point; it must not become a fixed invariant.

The migration threshold and chunk size are distinct parameters even if their default values are derived from one another.

3. Orchestration Layer

Responsibilities

- Select the placement policy for each file.
- Translate logical filesystem operations into Distribution layer requests.
- Coordinate operations between layers.
- Track available capacity and writable storage.
- Aggregate storage status and physical fault information.
- Decide the system-level response to storage faults.
- Maintain the in-memory state required to answer requests without unnecessary lower-layer I/O.

Policy selection

Placement policies should be selectable per file or file category rather than requiring one layout for the entire filesystem.

Policy selection may eventually use criteria such as:

- File size.
- Directory or file category.
- User-defined attributes.
- Desired redundancy and performance characteristics.
- Available storage capacity and drive health.

For example, small configuration files may use mirroring while large media files use striping with parity. These are policy examples, not mandatory file-type rules.

The selection of a policy must be separate from the implementation of the placement scheme and from the actual physical locations selected for a file.

Configuration changes

Changing a policy affects new files and future placement decisions unless an explicit migration is requested.

A configuration change must not cause existing data to be interpreted using a different layout from the one under which it was stored.

4. FUSE Layer

Responsibilities

- Expose the filesystem to the operating system through FUSE.
- Translate filesystem operations into requests to the Orchestration layer.
- Return operation results and errors to the FUSE caller.
- Keep policy and physical storage implementation details out of the interface layer.

Constraints

- Contains minimal policy logic.
- Does not implement striping, mirroring, parity or physical placement.
- Does not directly manage physical files.

Error Handling

Errors must distinguish between the outcome of an operation and notification of a storage fault.

Operation errors

A layer returns an error when the requested operation fails at that layer.

A lower-layer I/O error must not automatically be discarded merely because it represents a recoverable physical fault.

The Distribution layer may use redundancy to recover from an individual I/O failure. It determines whether the requested operation can still succeed and returns the corresponding operation result.

Fault reporting

A "reportError" callback may notify the Orchestration layer of a physical or storage-related fault.

Fault reporting is separate from returning the operation result. Reporting a fault does not replace the obligation to return an accurate result for the requested operation.

The Orchestration layer is responsible for deciding the broader response, such as updating drive status, preventing further placement on a faulty drive, or marking storage as degraded.

Fatal errors

Fatal internal errors must be propagated rather than treated as ordinary recoverable drive failures. Examples include unrecoverable internal state corruption or failures that prevent the system from safely continuing.

The precise classification of fatal errors, recoverable faults and degraded-but-successful operations remains to be defined.

Metadata and Consistency

Each file must have enough persistent metadata to identify its logical properties and actual physical layout.

The initial design favors embedding metadata at the beginning of the file's physical representation rather than depending on a separate database as the sole source of truth.

The previously estimated metadata size of 368 bytes is not a fixed requirement. The layout may change as the required fields and integrity mechanisms become clearer.

Metadata may need to describe:

- File identity and logical size.
- Format and layout versions.
- Placement scheme and its parameters.
- Participating drives and physical chunk locations.
- Redundancy and recovery information.
- Generation or migration state.
- Future integrity or encryption information, if adopted.

The metadata format and its storage arrangement must be designed together with physical block alignment and crash recovery.

Metadata updates must not expose a partially committed layout after a crash. A transactional or copy-on-write metadata scheme should be selected before implementing operations that change a file's physical representation.

A separate metadata database is not currently a design requirement. Any in-memory index or cache must be reconstructible from persistent storage metadata or other explicitly defined recovery information.

Deferred Decisions

The following items are intentionally not fixed at this stage:

- Encryption and authentication.
- Exact metadata format and size.
- Metadata commit and crash-recovery protocol.
- Exact parity algorithm and supported redundancy layouts.
- Default migration threshold and stripe chunk size.
- Dynamic policy selection rules.
- Concurrency control for simultaneous writes and migrations.
- Exact fatal-error classification and fault-state transitions.

These decisions should be made without compromising the four-layer architecture or the ability to read existing files after configuration changes.
