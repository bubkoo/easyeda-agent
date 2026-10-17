# EasyEDA Connector Contract

The connector is an EasyEDA Pro extension installed in the official client. It is the only part of the system that can access the official `eda` object.

## Runtime Constraints

The connector runs inside EasyEDA's webview, which shapes the transport:

- **No `fetch` to the daemon.** EasyEDA Pro is served over HTTPS, so the browser blocks mixed-content requests to `http://127.0.0.1`. The connector cannot use the `/health` endpoint for discovery.
- **No browser `WebSocket`.** The connector must use the official `eda.sys_WebSocket` API (`register` / `send` / `close`).
- **Permission gate.** The user must enable the extension's **Allow external interaction** (允许外部交互) permission, or every `eda.sys_WebSocket` call throws.
- The connector cannot write to the daemon's filesystem; binary artifacts are sent inline (see [protocol.md](protocol.md)).

## Startup

1. For each port in `127.0.0.1:60832-60841` (`0xEDA0`-`0xEDA9`), open a WebSocket to `ws://127.0.0.1:PORT/eda` via `eda.sys_WebSocket.register`.
2. Wait briefly (~1.5s) for the daemon to send a `handshake` frame. Verify `service === "easyeda-agent"`.
3. On a valid handshake, generate a `windowId` and send `register`, then `context`.
4. Start a `ping`/`pong` heartbeat; on consecutive missed pongs or a socket error, re-scan and reconnect.

## Required Messages

### handshake (daemon → connector)

Sent by the daemon immediately on connect so the connector can confirm it reached an easyeda-agent daemon before registering.

```json
{
  "type": "handshake",
  "service": "easyeda-agent",
  "version": "0.1.0-dev"
}
```

### register (connector → daemon)

```json
{
  "type": "register",
  "windowId": "uuid",
  "connectorVersion": "0.1.0",
  "easyedaVersion": "3.x",
  "capabilities": ["schematic.v1"],
  "userAgent": "Mozilla/5.0 ... edadesk/0.0.1 Chrome/152 Electron/44.4.5 ..."
}
```

`userAgent` 可选，是宿主页面原始 `navigator.userAgent`；连接器不做判断，daemon 分类为
`health.windows[].host.kind`（`edadesk` / `electron` / `browser` / `unknown`），见
[连接器宿主](concepts.md#连接器宿主connector-host)。

### context (connector → daemon)

```json
{
  "type": "context",
  "windowId": "uuid",
  "projectUuid": "...",
  "documentUuid": "...",
  "documentType": "schematic"
}
```

### ping / pong (heartbeat)

The connector sends `ping` periodically; the daemon answers `pong` echoing the `id`. Consecutive missed pongs trigger reconnect. Each extension activation uses a distinct host WebSocket id because EasyEDA 3.2.175 can activate one extension more than once in a window; sharing a fixed id would make those activations close each other's connection.

```json
{ "type": "ping", "id": "hb-1" }
```

```json
{ "type": "pong", "id": "hb-1" }
```

### action result

Use the response envelope defined in [protocol.md](protocol.md).

`runAction` samples the official project and active document immediately before
and after the FIFO handler. It sends additive `mutationContext`,
`mutationContextAvailable`, and `mutationContextError` alongside the diagnostic
`context`. Certification requires matching nonempty project/document/type and
symmetrically present, matching optional tab identities. A handler's own target
readback must also agree; an explicit unknown target cannot be replaced by an
ambient sample. Missing identity or drift sets availability to false without
changing the native result or claiming rollback. Boundary sampling does not
detect ABA switches during the handler.

The transport must preserve explicit false on the wire. A daemon must not use
post-action `context` or missing legacy certification as a mutation/save target.
For target-bound autosave, `schematic.save` / `pcb.save` check `expectedContext`
inside the FIFO before calling the SDK. Successful native save results remain
visible even when identity certification is unavailable; only certified saves
cancel the matching older timer. Warnings, health, and audit retain unavailable
identity evidence.

## Official API Mapping for Phase 1

The connector will map actions to these EasyEDA APIs first:

- `eda.dmt_Project.getCurrentProjectInfo`
- `eda.dmt_SelectControl.getCurrentDocumentInfo`
- `eda.dmt_Schematic.getAllSchematicsInfo`
- `eda.dmt_Schematic.getAllSchematicPagesInfo`
- `eda.dmt_EditorControl.openDocument`
- `eda.dmt_EditorControl.getCurrentRenderedAreaImage`
- `eda.sch_PrimitiveComponent.getAll`
- `eda.sch_PrimitiveComponent.create`
- `eda.sch_PrimitiveComponent.modify`
- `eda.sch_PrimitiveComponent.delete`
- `eda.sch_PrimitiveWire.create`
- `eda.sch_PrimitiveComponent.createNetFlag`
- `eda.sch_PrimitiveComponent.createNetPort`
- `eda.sch_SelectControl.doSelectPrimitives`
- `eda.sch_SelectControl.getSelectedPrimitives_PrimitiveId`
- `eda.sch_Drc.check`
- `eda.sch_Document.save`
- `eda.sch_ManufactureData.getNetlistFile`
- `eda.sch_ManufactureData.getBomFile`

## Serialization Rules

- Convert primitive objects to plain JSON before returning.
- Include primitive ID, type, common coordinates, designator, net, and pins when available.
- Convert `File` and `Blob` to artifacts, not inline JSON.
- Preserve the original EasyEDA error message in `error.detail`.
- Add a stable `error.code` chosen by the connector or daemon.
