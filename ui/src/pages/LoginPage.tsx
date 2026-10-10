import { useState } from 'react'
import type { FormEvent, ReactElement } from 'react'
import { ApiError } from '../api/client'
import { verifyToken } from '../api/httpClient'
import { Alert } from '../components/ui/Alert'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { TextField } from '../components/forms/TextField'

export interface LoginPageProps {
  /** Called after the token has been verified against the appliance. */
  readonly onLogin: (token: string) => void
}

/**
 * Token login: the appliance's bearer token is the only credential. The
 * token is verified with a real config read before a session is opened —
 * a wrong token never reaches the wired client.
 */
export function LoginPage({ onLogin }: LoginPageProps): ReactElement {
  const [token, setToken] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | undefined>(undefined)

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault()
    const candidate = token.trim()
    if (candidate === '') {
      setError('Enter the appliance API token.')
      return
    }
    setSubmitting(true)
    setError(undefined)
    try {
      await verifyToken('', candidate)
      onLogin(candidate)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setError('That token was rejected by the appliance.')
      } else {
        setError(err instanceof Error ? err.message : String(err))
      }
      setSubmitting(false)
    }
  }

  return (
    <div className="axw-login">
      <Card title="Sign in to AxonWall">
        <p className="axw-hint">
          Use the appliance API token — printed on first boot, or set during first-boot setup.
        </p>
        {error !== undefined && (
          <Alert severity="error" title="Sign-in failed" message={error} onDismiss={() => setError(undefined)} />
        )}
        <form className="axw-field" onSubmit={(e) => void submit(e)}>
          <TextField
            label="API token"
            value={token}
            onChange={(value) => {
              setToken(value)
              if (error !== undefined) setError(undefined)
            }}
            placeholder="axonwall API token"
            mono
            disabled={submitting}
          />
          <div className="axw-form-actions">
            <Button variant="primary" type="submit" disabled={submitting}>
              {submitting ? 'Signing in…' : 'Sign in'}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  )
}
