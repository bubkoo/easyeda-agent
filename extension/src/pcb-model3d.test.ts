import assert from 'node:assert/strict';
import { test } from 'node:test';
import { applyTransformPatch, formatTransform, MODEL_KEY, MODEL_TITLE_KEY, parseTransform, pcbModel3dBind, pcbModel3dList, pcbModel3dSetTransform, TRANSFORM_KEY } from './pcb-model3d';

type Part = { id: string; ref: string; props?: Record<string, unknown>; model?: unknown };
function host(parts: Part[], opts: { dropWrite?: boolean; wipeKey?: string; ignoreBind?: boolean } = {}) {
	const modifies: unknown[] = [];
	const comp = (p: Part) => ({
		getState_PrimitiveId: () => p.id, getState_Designator: () => p.ref, getState_Layer: () => 1,
		getState_Footprint: () => ({ uuid: 'fp', libraryUuid: 'l', name: 'R0402' }),
		getState_Model3D: () => p.model ?? (typeof p.props?.[MODEL_KEY] === 'string' && !opts.ignoreBind ? { uuid: p.props[MODEL_KEY], libraryUuid: String(p.props[MODEL_KEY]).split('|')[1] } : undefined), getState_OtherProperty: () => p.props ? { ...p.props } : undefined,
	});
	(globalThis as any).EDMT_EditorDocumentType = { PCB: 3 };
	(globalThis as any).eda = {
			lib_3DModel: { get: async (uuid: string, lib: string) => uuid === 'm9' && lib === 'L' ? { uuid, libraryUuid: lib, name: 'SOT-23' } : undefined },
	dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'p' }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'b', documentType: 3, tabId: 't' }) },
		pcb_PrimitiveComponent: {
			getAll: async () => parts.map(comp),
			get: async (id: string) => { const p = parts.find(x => x.id === id); return p && comp(p); },
			modify: async (id: string, prop: { otherProperty: Record<string, unknown> }) => {
				modifies.push(prop);
				const p = parts.find(x => x.id === id)!;
				if (!opts.dropWrite) p.props = { ...prop.otherProperty };
				if (opts.wipeKey) delete p.props![opts.wipeKey];
				return comp(p);
			},
		},
	};
	return modifies;
}

const T = '39.37,19.685,0,0,0,0,0,0.0005,0';

test('transform string round-trips and patches only named axes', () => {
	const t = parseTransform(T)!;
	assert.deepEqual(t.size, { x: 39.37, y: 19.685, z: 0 });
	assert.equal(formatTransform(t), T);
	assert.equal(formatTransform(applyTransformPatch(t, { offset: { z: -118.11 }, rotation: { x: 180 } })), '39.37,19.685,0,180,0,0,0,0.0005,-118.11');
	for (const bad of [undefined, '', '1,2,3', '1,2,3,4,5,6,7,8,x', '1,2,3,4,5,6,7,8,']) assert.equal(parseTransform(bad), undefined);
});

test('list reports bound/unbound/unknown and transform status without dropping unreadable parts', async () => {
	host([
		{ id: 'a', ref: 'R1', props: { [TRANSFORM_KEY]: T, Height: '0.4mm' }, model: { uuid: 'm|l', libraryUuid: 'l', name: 'R0402' } },
		{ id: 'b', ref: 'R10', props: {}, model: undefined },
		{ id: 'c', ref: 'R2', props: { [TRANSFORM_KEY]: 'junk' }, model: { uuid: 'm2', libraryUuid: 'l' } },
	]);
	const res = await pcbModel3dList({});
	const comps = res.result!.components as any[];
	assert.deepEqual(comps.map(c => c.designator), ['R1', 'R2', 'R10']);
	assert.equal(comps[0].model.uuid, 'm');
	assert.equal(comps[0].height, '0.4mm');
	assert.deepEqual(comps.map(c => c.transformStatus), ['ok', 'malformed', 'missing']);
	assert.deepEqual(res.result!.summary, { total: 3, bound: 2, unbound: 1, unknown: 0 });
});

test('set_transform writes one key, keeps others, strips projected keys and verifies', async () => {
	const parts: Part[] = [{ id: 'a', ref: 'J1', props: { [TRANSFORM_KEY]: T, Height: '1mm', Designator: 'J?' } }];
	const modifies = host(parts);
	const res = await pcbModel3dSetTransform({ designator: 'J1', patch: { offset: { z: -118.11 } } });
	assert.equal(res.result!.verified, true);
	assert.equal(parts[0].props![TRANSFORM_KEY], '39.37,19.685,0,0,0,0,0,0.0005,-118.11');
	assert.equal(parts[0].props!.Height, '1mm');
	assert.equal((modifies[0] as any).designator, 'J1');
	assert.equal('Designator' in (modifies[0] as any).otherProperty, false);
});

test('set_transform dry-run and noop never call modify', async () => {
	const modifies = host([{ id: 'a', ref: 'J1', props: { [TRANSFORM_KEY]: T } }]);
	assert.equal((await pcbModel3dSetTransform({ designator: 'J1', patch: { size: { x: 50 } }, dryRun: true })).result!.dryRun, true);
	assert.equal((await pcbModel3dSetTransform({ designator: 'J1', patch: { size: { x: 39.37 } } })).result!.noop, true);
	assert.equal(modifies.length, 0);
});

test('set_transform refuses ambiguous/missing targets and bad patches before writing', async () => {
	const modifies = host([{ id: 'a', ref: 'U1', props: { [TRANSFORM_KEY]: T } }, { id: 'b', ref: 'U1', props: {} }, { id: 'c', ref: 'R1', props: {} }]);
	await assert.rejects(pcbModel3dSetTransform({ designator: 'U1', patch: { size: { x: 1 } } }), /exactly one/);
	await assert.rejects(pcbModel3dSetTransform({ designator: 'R1', patch: { size: { x: 1 } } }), /bind a model first/);
	for (const patch of [{}, { scale: { x: 1 } }, { size: { w: 1 } }, { size: { x: Number.NaN } }]) {
		await assert.rejects(pcbModel3dSetTransform({ designator: 'R1', patch }));
	}
	assert.equal(modifies.length, 0);
});

test('set_transform reports partial with before-state when readback disagrees', async () => {
	for (const opts of [{ dropWrite: true }, { wipeKey: 'Height' }]) {
		host([{ id: 'a', ref: 'J1', props: { [TRANSFORM_KEY]: T, Height: '1mm' } }], opts);
		const res = await pcbModel3dSetTransform({ designator: 'J1', patch: { offset: { z: 5 } } });
		assert.equal(res.result!.partial, true);
		assert.equal((res.result!.otherPropertyBefore as any).Height, '1mm');
	}
});

test('bind writes model+title only and verifies through getState_Model3D', async () => {
	const parts: Part[] = [{ id: 'a', ref: 'U1', props: { [TRANSFORM_KEY]: T, Height: '1mm' } }];
	host(parts);
	const res = await pcbModel3dBind({ designator: 'U1', model: { uuid: 'm9', libraryUuid: 'L' } });
	assert.equal(res.result!.verified, true);
	assert.equal(parts[0].props![MODEL_KEY], 'm9|L');
	assert.equal(parts[0].props![MODEL_TITLE_KEY], 'SOT-23');
	assert.equal(parts[0].props![TRANSFORM_KEY], T);
	assert.equal((await pcbModel3dBind({ designator: 'U1', model: { uuid: 'm9', libraryUuid: 'L' } })).result!.noop, true);
});

test('bind refuses unknown models and reports host-ignored bindings as partial', async () => {
	const modifies = host([{ id: 'a', ref: 'U1', props: {} }]);
	await assert.rejects(pcbModel3dBind({ designator: 'U1', model: { uuid: 'nope', libraryUuid: 'L' } }), /not found/);
	await assert.rejects(pcbModel3dBind({ designator: 'U1', model: { uuid: 'm9|L', libraryUuid: 'L' } }));
	assert.equal(modifies.length, 0);
	host([{ id: 'a', ref: 'U1', props: {} }], { ignoreBind: true });
	const res = await pcbModel3dBind({ designator: 'U1', model: { uuid: 'm9', libraryUuid: 'L' } });
	assert.equal(res.result!.partial, true);
	assert.match(String((res.result!.problems as string[]).join()), /getState_Model3D/);
});
