import { useState } from 'react'
import type { ReactElement } from 'react'
import type { AxonWallConfig, DhcpPool, WgPeer } from '../api/types'
import { DhcpPanel } from '../components/services/DhcpPanel'
import { DhcpPoolEditorForm } from '../components/services/DhcpPoolEditorForm'
import { DnsPanel } from '../components/services/DnsPanel'
import { WireGuardPanel } from '../components/services/WireGuardPanel'
import { WireGuardPeerEditorForm } from '../components/services/WireGuardPeerEditorForm'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { ConfirmDialog } from '../components/ui/ConfirmDialog'
import { removeDhcpPool, setDhcpPool, setDns, setWireGuard } from '../model/updaters'
import { validationContext } from '../validation/validate'
import type { DhcpPoolValues } from '../validation/validate'

export interface ServicesPageProps {
  readonly config: AxonWallConfig
  readonly saving: boolean
  readonly onChange: (next: AxonWallConfig) => void
}

type PoolEditorState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'new' }
  | { readonly kind: 'edit'; readonly index: number }
  | { readonly kind: 'delete'; readonly index: number }

type PeerEditorState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'new' }
  | { readonly kind: 'edit'; readonly index: number }

export function ServicesPage({ config, saving, onChange }: ServicesPageProps): ReactElement {
  const [poolEditor, setPoolEditor] = useState<PoolEditorState>({ kind: 'closed' })
  const [peerEditor, setPeerEditor] = useState<PeerEditorState>({ kind: 'closed' })

  const ctx = validationContext(config)
  const dns = config.services?.dns
  const pools: readonly DhcpPool[] = config.services?.dhcp?.pools ?? []
  const wireguard = config.services?.wireguard
  const peers: readonly WgPeer[] = wireguard?.peers ?? []

  const poolEditIndex = poolEditor.kind === 'edit' ? poolEditor.index : undefined
  const editPool = poolEditIndex === undefined ? undefined : pools[poolEditIndex]
  const otherPools = pools.filter((_, i) => i !== poolEditIndex)

  const deletePoolIndex = poolEditor.kind === 'delete' ? poolEditor.index : undefined
  const deletePool = deletePoolIndex === undefined ? undefined : pools[deletePoolIndex]

  const peerEditIndex = peerEditor.kind === 'edit' ? peerEditor.index : undefined
  const editPeer = peerEditIndex === undefined ? undefined : peers[peerEditIndex]
  const takenPeerNames = peers.filter((_, i) => i !== peerEditIndex).map((p) => p.name)
  const takenPeerKeys = peers.filter((_, i) => i !== peerEditIndex).map((p) => p.publicKey)

  const savePool = (index: number | null) => (values: DhcpPoolValues): void => {
    const pool: DhcpPool = {
      zone: values.zone,
      range: [values.start.trim(), values.end.trim()],
      gateway: values.gateway.trim(),
      dns: values.dns.trim(),
    }
    onChange(setDhcpPool(config, index, pool))
    setPoolEditor({ kind: 'closed' })
  }

  const setPeers = (nextPeers: readonly WgPeer[]): void => {
    if (wireguard === undefined) return
    onChange(setWireGuard(config, { listenPort: wireguard.listenPort, peers: nextPeers }))
  }

  return (
    <div>
      <h1 className="axw-page-title">Services</h1>

      {dns !== undefined && (
        <Card title="DNS">
          <DnsPanel
            dns={dns}
            zoneNames={ctx.zoneNames}
            onChange={(next) => onChange(setDns(config, next))}
            disabled={saving}
          />
        </Card>
      )}

      <Card title="DHCP">
        <DhcpPanel
          pools={pools}
          disabled={saving}
          onEdit={(index) => setPoolEditor({ kind: 'edit', index })}
          onDelete={(index) => setPoolEditor({ kind: 'delete', index })}
        />
        <div className="axw-form-actions">
          <Button variant="primary" disabled={saving} onClick={() => setPoolEditor({ kind: 'new' })}>
            Add DHCP pool
          </Button>
        </div>
      </Card>

      {poolEditor.kind === 'new' && (
        <Card title="New DHCP pool">
          <DhcpPoolEditorForm
            ctx={ctx}
            others={otherPools}
            disabled={saving}
            onCancel={() => setPoolEditor({ kind: 'closed' })}
            onSave={savePool(null)}
          />
        </Card>
      )}

      {poolEditor.kind === 'edit' && editPool !== undefined && (
        <Card title={`Edit DHCP pool: ${editPool.zone}`}>
          <DhcpPoolEditorForm
            initial={{
              zone: editPool.zone,
              start: editPool.range[0],
              end: editPool.range[1],
              gateway: editPool.gateway,
              dns: editPool.dns,
            }}
            ctx={ctx}
            others={otherPools}
            disabled={saving}
            onCancel={() => setPoolEditor({ kind: 'closed' })}
            onSave={savePool(poolEditor.index)}
          />
        </Card>
      )}

      {deletePool !== undefined && deletePoolIndex !== undefined && (
        <ConfirmDialog
          title="Delete DHCP pool"
          message={`Delete the pool for zone "${deletePool.zone}"? Applied on the next config save.`}
          confirmLabel="Delete"
          disabled={saving}
          onCancel={() => setPoolEditor({ kind: 'closed' })}
          onConfirm={() => {
            onChange(removeDhcpPool(config, deletePoolIndex))
            setPoolEditor({ kind: 'closed' })
          }}
        />
      )}

      {wireguard !== undefined && (
        <Card title="WireGuard">
          <WireGuardPanel
            wg={wireguard}
            disabled={saving}
            onPortChange={(port) => onChange(setWireGuard(config, { listenPort: port, peers }))}
            onPeersChange={(nextPeers) => setPeers(nextPeers)}
            onEditPeer={(index) => setPeerEditor({ kind: 'edit', index })}
            onAddPeer={() => setPeerEditor({ kind: 'new' })}
          />
        </Card>
      )}

      {peerEditor.kind === 'new' && wireguard !== undefined && (
        <Card title="New WireGuard peer">
          <WireGuardPeerEditorForm
            takenNames={takenPeerNames}
            takenKeys={takenPeerKeys}
            disabled={saving}
            onCancel={() => setPeerEditor({ kind: 'closed' })}
            onSave={(peer) => {
              setPeers([...peers, peer])
              setPeerEditor({ kind: 'closed' })
            }}
          />
        </Card>
      )}

      {peerEditor.kind === 'edit' && editPeer !== undefined && wireguard !== undefined && (
        <Card title={`Edit WireGuard peer: ${editPeer.name}`}>
          <WireGuardPeerEditorForm
            initial={{ name: editPeer.name, publicKey: editPeer.publicKey, allowedIps: editPeer.allowedIps }}
            takenNames={takenPeerNames}
            takenKeys={takenPeerKeys}
            disabled={saving}
            onCancel={() => setPeerEditor({ kind: 'closed' })}
            onSave={(peer) => {
              const nextPeers = [...peers]
              nextPeers[peerEditor.index] = peer
              setPeers(nextPeers)
              setPeerEditor({ kind: 'closed' })
            }}
          />
        </Card>
      )}
    </div>
  )
}
