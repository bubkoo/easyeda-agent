import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

function host(renameOk: boolean) {
	const boards: Array<{ name: string; schematic: { uuid: string; name: string }; pcb: { uuid: string; name: string } }> = [];
	(globalThis as any).eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'proj' }) },
		dmt_Schematic: {
			createSchematic: async () => 'sch1',
			getSchematicInfo: async () => ({ page: [{ uuid: 'page1', name: 'P1' }] }),
		},
		dmt_Board: {
			getAllBoardsInfo: async () => boards,
			createBoard: async () => { boards.push({ name: 'Board3', schematic: { uuid: 'sch1', name: 'Schematic3' }, pcb: { uuid: '', name: '' } }); return 'Board3'; },
			modifyBoardName: async (_from: string, to: string) => { if (renameOk) boards[0].name = to; return renameOk; },
		},
		dmt_Pcb: {
			createPcb: async () => { boards[0].pcb = { uuid: 'pcb1', name: 'PCB3' }; return 'pcb1'; },
			getAllPcbsInfo: async () => [{ uuid: 'pcb1' }],
		},
	};
}

test('board.new reports a refused rename as partial with notApplied, keeping the created documents', async () => {
	host(false);
	const res: any = await runAction('board.new', { projectUuid: 'proj', name: 'Main' });
	assert.equal(res.result.boardName, 'Board3');
	assert.equal(res.result.verified, true);
	assert.equal(res.result.partial, true);
	assert.deepEqual(res.result.notApplied, ['name']);
	assert.equal(res.result.requestedName, 'Main');
});

test('board.new with an applied rename is fully verified', async () => {
	host(true);
	const res: any = await runAction('board.new', { projectUuid: 'proj', name: 'Main' });
	assert.equal(res.result.boardName, 'Main');
	assert.equal(res.result.partial, false);
	assert.equal(res.result.notApplied, undefined);
});
