import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TooltipProvider } from '@/components/ui/tooltip'
import { ApiError } from '@/lib/api'
import i18n from '@/i18n'
import { LoginForm } from './LoginForm'
import { login } from './api'

vi.mock('./api', () => ({ login: vi.fn() }))

function renderForm(onSuccess = vi.fn()) {
  render(
    <TooltipProvider>
      <LoginForm onSuccess={onSuccess} />
    </TooltipProvider>,
  )
  return onSuccess
}

describe('LoginForm', () => {
  beforeEach(async () => {
    vi.mocked(login).mockReset()
    await i18n.changeLanguage('id')
  })

  it('validates required fields without calling the API', async () => {
    renderForm()
    fireEvent.click(screen.getByRole('button', { name: 'Masuk' }))
    expect(await screen.findByText('Username wajib diisi')).toBeInTheDocument()
    expect(screen.getByText('Password wajib diisi')).toBeInTheDocument()
    expect(login).not.toHaveBeenCalled()
  })

  it('shows one generic message for wrong credentials', async () => {
    vi.mocked(login).mockRejectedValue(
      new ApiError('UNAUTHENTICATED', 401, 'username atau password salah'),
    )
    const onSuccess = renderForm()
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'nobody' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Masuk' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Username atau password salah.')
    expect(onSuccess).not.toHaveBeenCalled()
    expect(login).toHaveBeenCalledWith('nobody', 'secret')
  })

  it('shows the lockout wait and disables the button', async () => {
    vi.mocked(login).mockRejectedValue(
      new ApiError('ACCOUNT_LOCKED', 423, 'locked', { retryAfter: 60 }),
    )
    renderForm()
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: 'Masuk' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('60 detik')
    expect(screen.getByRole('button', { name: 'Masuk' })).toBeDisabled()
  })

  it('calls onSuccess with the login response and switches language', async () => {
    const response = { user: { username: 'admin' }, mustChangePassword: true, csrfToken: 't' }
    vi.mocked(login).mockResolvedValue(response as never)
    const onSuccess = renderForm()
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } })
    fireEvent.click(screen.getByRole('button', { name: 'Masuk' }))
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(response))
    await i18n.changeLanguage('en')
    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })
})
