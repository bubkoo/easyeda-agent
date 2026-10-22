/**
 * PROJECTED-STATE keys must NEVER be merged into a primitive's otherProperty
 * from a library record: the platform projects them from top-level primitive
 * state (designator, uniqueId, name, addIntoBom, manufacturerId, supplierId),
 * it does not store them in otherProperty. Two failure modes, both live-verified:
 *   - `Designator`: the library record carries its own PLACEHOLDER ("C?"), and
 *     the platform syncs otherProperty.Designator INTO the primitive's
 *     designator on modify — one sync-attrs run wiped 166/166 real designators
 *     to U?/C?/RF? (each part flipping to its own library's placeholder
 *     prefix); deterministic on a quiet 6-part board.
 *   - The rest: writes are silently DROPPED (never appear in the next
 *     getState_OtherProperty), so merging them re-"fills" the same keys every
 *     run — a lying report and wasted writes, never a real backfill.
 *
 * ONE ruler for both sides: the PCB `sync-attrs` backfill and the schematic
 * place-time backfill (#186) share this set. A second copy would be a second
 * ruler, and the failure it guards against is designator-wiping.
 */
export const PROJECTED_STATE_KEYS: ReadonlySet<string> = new Set([
	'Designator', 'Unique ID', 'Name', 'Add into BOM',
	'Manufacturer', 'Manufacturer Part', 'Supplier', 'Supplier Part',
]);
