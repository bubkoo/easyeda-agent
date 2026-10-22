/// <reference types="@jlceda/pro-api-types" />
import { readResponseContext } from './eda-context';
import { ActionError, ErrorCodes, type ActionResult } from './protocol';
import { PROJECTED_STATE_KEYS } from './projected-keys';

/**
 * Per-instance 3D model state on the active PCB. The host keeps the instance's
 * model placement in otherProperty["3D Model Transform"] as nine numbers:
 * size XYZ, rotation XYZ, offset XYZ (mil / degrees). The mapping is inferred
 * from live samples (see docs/pcb-3d-design-detail.md) — reported as such.
 */
export const TRANSFORM_KEY = '3D Model Transform';
const AXES = ['x', 'y', 'z'] as const;
const GROUPS = ['size', 'rotation', 'offset'] as const;
type Vec = { x: number; y: number; z: number };
export type Transform = { size: Vec; rotation: Vec; offset: Vec };
export type TransformPatch = Partial<Record<typeof GROUPS[number], Partial<Vec>>>;

export function parseTransform(raw: unknown): Transform | undefined {
	if (typeof raw !== 'string') return undefined;
	const parts = raw.split(',');
	if (parts.length !== 9 || parts.some(p => p.trim() === '')) return undefined;
	const n = parts.map(Number);
	if (n.some(v => !Number.isFinite(v))) return undefined;
	const vec = (i: number): Vec => ({ x: n[i], y: n[i + 1], z: n[i + 2] });
	return { size: vec(0), rotation: vec(3), offset: vec(6) };
}

const fmt = (v: number) => String(Math.round(v * 1e4) / 1e4);
export function formatTransform(t: Transform): string {
	return GROUPS.flatMap(g => AXES.map(a => fmt(t[g][a]))).join(',');
}

export function applyTransformPatch(t: Transform, patch: TransformPatch): Transform {
	const next = JSON.parse(JSON.stringify(t)) as Transform;
	for (const g of GROUPS) for (const a of AXES) {
		const v = patch[g]?.[a];
		if (v !== undefined) next[g][a] = v;
	}
	return next;
}

function normalizePatch(raw: unknown): TransformPatch {
	if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing object "patch" ({size?,rotation?,offset?} with x/y/z numbers).');
	const patch: TransformPatch = {};
	let count = 0;
	for (const [g, vec] of Object.entries(raw as Record<string, unknown>)) {
		if (!(GROUPS as readonly string[]).includes(g) || !vec || typeof vec !== 'object' || Array.isArray(vec)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Unknown or malformed patch group "${g}".`);
		const out: Partial<Vec> = {};
		for (const [a, v] of Object.entries(vec as Record<string, unknown>)) {
			if (!(AXES as readonly string[]).includes(a) || typeof v !== 'number' || !Number.isFinite(v)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `patch.${g}.${a} must be a finite number.`);
			out[a as keyof Vec] = v;
			count++;
		}
		patch[g as keyof TransformPatch] = out;
	}
	if (!count) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'patch must set at least one size/rotation/offset axis.');
	return patch;
}

type Component = IPCB_PrimitiveComponent;
function otherProps(c: Component): Record<string, unknown> | undefined {
	try { return (c.getState_OtherProperty() as Record<string, unknown>) ?? {}; } catch { return undefined; }
}

function describe(c: Component) {
	const props = otherProps(c);
	let model: { uuid: string; libraryUuid: string; name: string | null; raw: string } | null = null;
	let status: 'bound' | 'unbound' | 'unknown' = 'unknown';
	try {
		const m = c.getState_Model3D();
		status = m?.uuid ? 'bound' : 'unbound';
		if (m?.uuid) model = { uuid: m.uuid.split('|')[0], libraryUuid: m.libraryUuid, name: m.name ?? null, raw: m.uuid };
	} catch { /* unknown stays in the denominator */ }
	const rawTransform = props?.[TRANSFORM_KEY];
	const parsed = parseTransform(rawTransform);
	let footprint = null;
	try { footprint = c.getState_Footprint() ?? null; } catch { /* optional */ }
	return {
		primitiveId: c.getState_PrimitiveId(), designator: c.getState_Designator() ?? null, layer: c.getState_Layer(),
		footprint, model, status,
		transform: parsed ? { raw: rawTransform, ...parsed } : null,
		transformStatus: props === undefined ? 'unknown' : rawTransform === undefined || rawTransform === '' ? 'missing' : parsed ? 'ok' : 'malformed',
		height: typeof props?.Height === 'string' && props.Height ? props.Height : null,
	};
}

async function requirePcb() {
	const ctx = await readResponseContext();
	if (ctx.documentType !== 'pcb') throw new ActionError(ErrorCodes.INVALID_STATE, 'Activate the target PCB before reading or editing 3D models.');
	const all = await eda.pcb_PrimitiveComponent.getAll();
	if (!Array.isArray(all)) throw new ActionError(ErrorCodes.INVALID_STATE, 'PCB component inventory unavailable.');
	return all;
}

export async function pcbModel3dList(payload: Record<string, unknown>): Promise<ActionResult> {
	if (Object.keys(payload).length) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'pcb.model3d.list takes no payload; filter on the CLI side.');
	const components = (await requirePcb()).map(describe)
		.sort((a, b) => String(a.designator).localeCompare(String(b.designator), undefined, { numeric: true }));
	const count = (s: string) => components.filter(c => c.status === s).length;
	return { result: {
		components, summary: { total: components.length, bound: count('bound'), unbound: count('unbound'), unknown: count('unknown') },
		transformUnit: 'mil', rotationUnit: 'deg', transformMapping: 'inferred',
	} };
}

/** Change only the named axes of one instance's model placement, verified by fresh readback. */
export async function pcbModel3dSetTransform(payload: Record<string, unknown>): Promise<ActionResult> {
	const patch = normalizePatch(payload.patch);
	const dryRun = payload.dryRun === true;
	const { designator, comp, primitiveId, before } = await targetComponent(payload);
	const current = parseTransform(before[TRANSFORM_KEY]);
	if (!current) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `${designator} has no readable "${TRANSFORM_KEY}" (${JSON.stringify(before[TRANSFORM_KEY] ?? null)}); bind a model first. Nothing written.`);
	const next = applyTransformPatch(current, patch);
	const written = formatTransform(next);
	const base = { designator, primitiveId, before: formatTransform(current), after: written, transform: next, unit: 'mil', rotationUnit: 'deg' };
	if (written === base.before) return { result: { ...base, noop: true, verified: true } };
	if (dryRun) return { result: { ...base, dryRun: true } };

	const problems = await writeOwnKeys(comp, designator, before, { [TRANSFORM_KEY]: written });
	if (problems.length) return partial(base, problems, before);
	return { result: { ...base, verified: true } };
}

function partial(base: Record<string, unknown>, problems: string[], before: Record<string, unknown>): ActionResult {
	return { result: { ...base, partial: true, verified: false, problems, otherPropertyBefore: before },
		warnings: ['Readback differs from the requested change; do not retry blindly — restore from otherPropertyBefore if needed.'] };
}

/**
 * Write only `changes` into one instance's otherProperty (projected keys never
 * written, designator re-asserted), then compare a fresh readback key by key.
 * Returns the problems found; an empty list means verified.
 */
async function writeOwnKeys(comp: Component, designator: string, before: Record<string, unknown>, changes: Record<string, string>): Promise<string[]> {
	const primitiveId = comp.getState_PrimitiveId();
	const merged: Record<string, unknown> = {};
	for (const [k, v] of Object.entries(before)) if (!PROJECTED_STATE_KEYS.has(k)) merged[k] = v;
	Object.assign(merged, changes);
	try { await eda.pcb_PrimitiveComponent.modify(primitiveId, { designator, otherProperty: merged }); }
	catch (err) { throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `modify failed for ${designator}; inspect with pcb 3d models before retrying.`, String(err)); }

	const fresh = await eda.pcb_PrimitiveComponent.get(primitiveId);
	const after = fresh ? otherProps(fresh) : undefined;
	if (!fresh || !after) return ['fresh readback unavailable'];
	const problems: string[] = [];
	if (fresh.getState_Designator() !== designator) problems.push(`designator now ${JSON.stringify(fresh.getState_Designator())}`);
	for (const k of new Set([...Object.keys(before), ...Object.keys(after), ...Object.keys(changes)])) {
		if (PROJECTED_STATE_KEYS.has(k)) continue;
		const want = k in changes ? changes[k] : before[k];
		if (JSON.stringify(want) !== JSON.stringify(after[k])) problems.push(k in changes ? `"${k}" reads ${JSON.stringify(after[k])}` : `otherProperty "${k}" changed`);
	}
	return problems;
}

async function targetComponent(payload: Record<string, unknown>) {
	const designator = payload.designator;
	if (typeof designator !== 'string' || !designator.trim()) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required string "designator".');
	const matches = (await requirePcb()).filter(c => c.getState_Designator() === designator);
	if (matches.length !== 1) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Designator "${designator}" matches ${matches.length} PCB components; need exactly one.`);
	const comp = matches[0];
	const before = otherProps(comp);
	if (!before) throw new ActionError(ErrorCodes.INVALID_STATE, `Cannot read otherProperty of ${designator}; nothing written.`);
	return { designator, comp, primitiveId: comp.getState_PrimitiveId(), before };
}

export const MODEL_KEY = '3D Model';
export const MODEL_TITLE_KEY = '3D Model Title';

/**
 * Bind (or replace) the library 3D model of ONE PCB instance. The host has no
 * dedicated setter; the instance binding lives in otherProperty["3D Model"]
 * ("uuid|libraryUuid") and ["3D Model Title"]. Verified by getState_Model3D.
 * The placement is kept as-is: re-check it with pcb 3d models afterwards.
 */
export async function pcbModel3dBind(payload: Record<string, unknown>): Promise<ActionResult> {
	const raw = payload.model as Record<string, unknown> | undefined;
	if (!raw || typeof raw.uuid !== 'string' || !raw.uuid || raw.uuid.includes('|') || typeof raw.libraryUuid !== 'string' || !raw.libraryUuid) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing object "model" {uuid, libraryUuid} (plain UUIDs).');
	}
	const uuid = raw.uuid, libraryUuid = raw.libraryUuid;
	const { designator, comp, primitiveId, before } = await targetComponent(payload);
	const model = await eda.lib_3DModel.get(uuid, libraryUuid);
	if (!model) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `3D model ${uuid} not found in library ${libraryUuid}; nothing written.`);
	const value = `${uuid}|${libraryUuid}`;
	const base = { designator, primitiveId, model: { uuid, libraryUuid, name: model.name },
		previous: { [MODEL_KEY]: before[MODEL_KEY] ?? null, [MODEL_TITLE_KEY]: before[MODEL_TITLE_KEY] ?? null } };
	if (before[MODEL_KEY] === value && before[MODEL_TITLE_KEY] === model.name) return { result: { ...base, noop: true, verified: true } };
	if (payload.dryRun === true) return { result: { ...base, dryRun: true } };

	const problems = await writeOwnKeys(comp, designator, before, { [MODEL_KEY]: value, [MODEL_TITLE_KEY]: model.name });
	const fresh = await eda.pcb_PrimitiveComponent.get(primitiveId);
	let bound: { uuid?: string; libraryUuid?: string } | undefined;
	try { bound = fresh?.getState_Model3D(); } catch { /* reported below */ }
	if (bound?.uuid?.split('|')[0] !== uuid || bound?.libraryUuid !== libraryUuid) problems.push(`getState_Model3D reads ${JSON.stringify(bound ?? null)}`);
	if (problems.length) return partial(base, problems, before);
	return { result: { ...base, verified: true },
		warnings: ['Model placement was kept from the previous binding; review size/offset with pcb 3d models.'] };
}
