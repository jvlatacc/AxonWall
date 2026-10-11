// Navigation model — pure data shared by the shell and the app container.

export type PageId =
  | 'dashboard'
  | 'firewall'
  | 'nat'
  | 'aliases'
  | 'services'
  | 'status'
  | 'backup'

export interface NavItem {
  readonly id: PageId
  readonly label: string
}

export const NAV_ITEMS: readonly NavItem[] = [
  { id: 'dashboard', label: 'Dashboard' },
  { id: 'firewall', label: 'Firewall' },
  { id: 'nat', label: 'NAT' },
  { id: 'aliases', label: 'Aliases' },
  { id: 'services', label: 'Services' },
  { id: 'status', label: 'Status' },
  { id: 'backup', label: 'Backup' },
]
