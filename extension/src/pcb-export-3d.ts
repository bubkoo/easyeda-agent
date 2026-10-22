/// <reference types="@jlceda/pro-api-types" />
import { readResponseContext, mutationContextError } from './eda-context';
import { ActionError, ErrorCodes, type ActionResult } from './protocol';
import { blobToBase64, newArtifactId } from './util';

export const MAX_3D_BYTES = 20 * 1024 * 1024;
const ELEMENTS = ['Component Model', 'Via', 'Silkscreen', 'Wire In Signal Layer'] as const;

/** Preserve the official file, including a ZIP container if the host returns one. */
export async function pcbExport3D(payload: Record<string, unknown>): Promise<ActionResult> {
	const allowed = ['fileType', 'fileName', 'modelMode', 'autoGenerateModels', 'elements'];
	if (Object.keys(payload).some(k => !allowed.includes(k))) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Unknown 3D export option.');
	const fileType = payload.fileType ?? 'obj', modelMode = payload.modelMode ?? 'Outfit';
	const autoGenerateModels = payload.autoGenerateModels ?? false;
	const fileName = payload.fileName ?? `board.${fileType}`;
	const elements = payload.elements ?? ['Component Model'];
	if (typeof fileType !== 'string' || !['obj', 'step'].includes(fileType) || !['Outfit', 'Parts'].includes(modelMode as string)
		|| typeof autoGenerateModels !== 'boolean' || typeof fileName !== 'string'
		|| !fileName.trim() || fileName.length > 128 || /[\\/\x00-\x1f]/.test(fileName)
		|| !Array.isArray(elements) || !elements.length || elements.length > ELEMENTS.length
		|| new Set(elements).size !== elements.length || elements.some(e=>!ELEMENTS.includes(e))) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Use fileType=obj|step, modelMode=Outfit|Parts, a basename, boolean autoGenerateModels and unique official element names.');
	}
	const before = await readResponseContext();
	if (before.documentType !== 'pcb' || !before.projectUuid || !before.documentUuid) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'Activate the target PCB before exporting 3D.');
	}
	if (typeof eda.pcb_ManufactureData?.get3DFile !== 'function') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'This host does not expose pcb_ManufactureData.get3DFile.');
	}
	const readModels = async () => {
		const components = await eda.pcb_PrimitiveComponent.getAll();
		if (!Array.isArray(components) || !components.length) throw new ActionError(ErrorCodes.INVALID_STATE, 'No readable PCB components.');
		return components.map(c => {
			const primitiveId = c.getState_PrimitiveId(), ref = c.getState_Designator();
			try {
				const model = c.getState_Model3D();
				return { primitiveId, ref, status: model?.uuid ? 'bound' : 'unbound', model: model ?? null };
			} catch { return { primitiveId, ref, status: 'unknown', model: null }; }
		}).sort((a,b)=>a.primitiveId.localeCompare(b.primitiveId));
	};
	const models = await readModels();
	const file = await eda.pcb_ManufactureData.get3DFile(fileName, fileType as 'obj' | 'step',
		elements, modelMode as 'Outfit' | 'Parts', autoGenerateModels);
	if (!file) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Official ${fileType.toUpperCase()} export returned no file. Try OBJ; STEP requires STEP source component models.`);
	if (!file.size) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Official ${fileType.toUpperCase()} export returned an empty file. Try OBJ or inspect the source models.`);
	if (file.size > MAX_3D_BYTES) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Official ${fileType.toUpperCase()} export is ${file.size} bytes, exceeding the 20 MiB transport limit. Try OBJ or reduce model complexity.`, JSON.stringify({ fileType, size:file.size, limit:MAX_3D_BYTES }));
	const modelsAfter = await readModels();
	const after = await readResponseContext();
	const drift = mutationContextError(before, after);
	if (drift) throw new ActionError(ErrorCodes.INVALID_STATE, `3D export target changed: ${drift}. No artifact accepted.`);
	if (JSON.stringify(models) !== JSON.stringify(modelsAfter)) throw new ActionError(ErrorCodes.INVALID_STATE, 'Component model bindings changed during 3D export. No artifact accepted.');
	const id = newArtifactId();
	return { result: { artifactId: id, fileType, fileName: file.name || fileName, size: file.size,
		modelMode, elements, autoGenerateModels, exportContext: before, models, modelCoverage: 'binding-only',
		modelCoverageNote: 'A bound model is not proof the host included it. Unbound models are omitted unless automatic placeholders were requested. STEP includes only STEP source models.' },
		artifacts: [{ id, kind: 'pcb_3d', fileName: file.name || fileName,
			mimeType: file.type || 'application/octet-stream', inlineBase64: await blobToBase64(file) }],
		warnings: ['3D geometry and model inclusion require independent inspection; this export does not verify manufacturing or electrical correctness.'] };
}

/**
 * Export the 3D enclosure designed on the active PCB (official beta
 * get3DShellFile). Shell design itself is GUI-only: no primitive API accepts
 * the shell layers, so an empty result means "no shell designed", not a retry.
 */
export async function pcbExport3DShell(payload: Record<string, unknown>): Promise<ActionResult> {
	if (Object.keys(payload).some(k => !['fileType', 'fileName'].includes(k))) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Unknown 3D shell export option.');
	const fileType = payload.fileType ?? 'stl';
	const fileName = payload.fileName ?? `shell.${fileType}`;
	if (typeof fileType !== 'string' || !['stl', 'step', 'obj'].includes(fileType)
		|| typeof fileName !== 'string' || !fileName.trim() || fileName.length > 128 || /[\\/\x00-\x1f]/.test(fileName)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Use fileType=stl|step|obj and a basename.');
	}
	const before = await readResponseContext();
	if (before.documentType !== 'pcb' || !before.projectUuid || !before.documentUuid) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'Activate the target PCB before exporting its 3D shell.');
	}
	if (typeof eda.pcb_ManufactureData?.get3DShellFile !== 'function') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'This host does not expose pcb_ManufactureData.get3DShellFile.');
	}
	const file = await eda.pcb_ManufactureData.get3DShellFile(fileName, fileType as 'stl' | 'step' | 'obj');
	if (!file || !file.size) throw new ActionError(ErrorCodes.INVALID_STATE, 'This PCB has no 3D shell to export (design it in the EasyEDA shell editor first; the API cannot create one).');
	if (file.size > MAX_3D_BYTES) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `3D shell export is ${file.size} bytes, exceeding the 20 MiB transport limit.`);
	const drift = mutationContextError(before, await readResponseContext());
	if (drift) throw new ActionError(ErrorCodes.INVALID_STATE, `3D shell export target changed: ${drift}. No artifact accepted.`);
	const id = newArtifactId();
	return { result: { artifactId: id, fileType, fileName: file.name || fileName, size: file.size, exportContext: before },
		artifacts: [{ id, kind: 'pcb_3d_shell', fileName: file.name || fileName,
			mimeType: file.type || 'application/octet-stream', inlineBase64: await blobToBase64(file) }] };
}
