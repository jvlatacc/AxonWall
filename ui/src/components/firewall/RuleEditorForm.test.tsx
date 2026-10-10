// Interaction tests: the form wires validateRule's pure functions into
// submit-time error display, and only calls onSave with a fully valid value
// object (verdict narrowed from '' to Verdict).
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { RuleEditorForm } from './RuleEditorForm'

const baseProps = {
  zones: ['wan', 'lan'],
  aliases: ['admin-hosts'],
  takenNames: [],
  onCancel: () => {},
}

describe('RuleEditorForm', () => {
  it('shows validation errors on an empty submit and does not save', () => {
    const onSave = vi.fn()
    render(<RuleEditorForm {...baseProps} onSave={onSave} />)

    fireEvent.click(screen.getByRole('button', { name: /save rule/i }))

    expect(screen.getByText('name must be set')).toBeInTheDocument()
    expect(screen.getByText('verdict must be accept, drop, or reject')).toBeInTheDocument()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('surfaces live errors after a failed submit', () => {
    const onSave = vi.fn()
    render(<RuleEditorForm {...baseProps} onSave={onSave} />)

    fireEvent.click(screen.getByRole('button', { name: /save rule/i }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'lan-to-wan' } })

    expect(screen.queryByText('name must be set')).toBeNull()
  })

  it('calls onSave with the narrowed verdict when the form is valid', () => {
    const onSave = vi.fn()
    render(<RuleEditorForm {...baseProps} onSave={onSave} />)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'lan-to-wan' } })
    fireEvent.change(screen.getByLabelText('From zone'), { target: { value: 'lan' } })
    fireEvent.change(screen.getByLabelText('To zone'), { target: { value: 'firewall' } })
    fireEvent.change(screen.getByLabelText('Service'), { target: { value: 'ssh' } })
    fireEvent.change(screen.getByLabelText('Source alias'), { target: { value: 'admin-hosts' } })
    fireEvent.change(screen.getByLabelText('Verdict'), { target: { value: 'accept' } })
    fireEvent.click(screen.getByRole('button', { name: /save rule/i }))

    expect(onSave).toHaveBeenCalledWith({
      name: 'lan-to-wan',
      from: 'lan',
      to: 'firewall',
      service: 'ssh',
      sourceAlias: 'admin-hosts',
      verdict: 'accept',
    })
  })
})
