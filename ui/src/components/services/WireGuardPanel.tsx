import type { ReactElement } from 'react'
import type { WgPeer, WireGuardService } from '../../api/types'
import { Button } from '../ui/Button'
import { TextField } from '../forms/TextField'
import { WireGuardPeersTable } from './WireGuardPeersTable'

export interface WireGuardPanelProps {
  readonly wg: WireGuardService
  readonly onPortChange: (port: number) => void
  readonly onPeersChange: (peers: WgPeer[]) => void
  readonly onEditPeer?: (index: number) => void
  readonly onDeletePeer?: (index: number) => void
  readonly onAddPeer?: () => void
  readonly disabled?: boolean
}

export function WireGuardPanel({
  wg,
  onPortChange,
  onPeersChange,
  onEditPeer,
  onDeletePeer,
  onAddPeer,
  disabled = false,
}: WireGuardPanelProps): ReactElement {
  const hasActions = onEditPeer !== undefined || onDeletePeer !== undefined || onAddPeer !== undefined
  return (
    <div>
      <TextField
        label="Listen port"
        value={String(wg.listenPort)}
        onChange={(text) => {
          const port = Number.parseInt(text, 10)
          if (!Number.isNaN(port)) onPortChange(port)
        }}
        hint="UDP port WireGuard listens on"
        mono
        disabled={disabled}
      />
      <h4 className="axw-sub-heading">Peers</h4>
      <WireGuardPeersTable
        peers={wg.peers}
        onEdit={onEditPeer}
        onDelete={
          onDeletePeer === undefined
            ? undefined
            : (index) => onPeersChange(wg.peers.filter((_, i) => i !== index))
        }
        disabled={disabled}
      />
      {hasActions && onAddPeer !== undefined && (
        <div className="axw-form-actions">
          <span>
            <Button variant="secondary" disabled={disabled} onClick={onAddPeer}>
              Add peer
            </Button>
          </span>
        </div>
      )}
    </div>
  )
}
