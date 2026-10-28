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

const totpRequired = () =>
  new ApiError('VALIDATION_FAILED', 400, 'code required', { fields: { totp: 'totp_required' } })
const totpInvalid = () =>
  new ApiError('VALIDATION_FAILED', 400, 'code invalid', { fields: { totp: 'totp_invalid' } })

/** Fills the credentials and submits; resolves once the second step is shown. */
async function reachSecondStep() {
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'budi' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } })
  fireEvent.click(screen.getByRole('button', { name: 'Masuk' }))
  return screen.findByLabelText('Kode verifikasi')
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

  describe('two-step verification (Step 23a)', () => {
    it('asks for the code after totp_required and resubmits the credentials with it', async () => {
      const response = { user: { username: 'budi' }, mustChangePassword: false, csrfToken: 't' }
      vi.mocked(login)
        .mockRejectedValueOnce(totpRequired())
        .mockResolvedValueOnce(response as never)
      const onSuccess = renderForm()
      const input = await reachSecondStep()
      expect(input).toHaveFocus()
      expect(input).toHaveAttribute('inputmode', 'numeric')
      expect(input).toHaveAttribute('autocomplete', 'one-time-code')
      expect(screen.queryByLabelText('Username')).not.toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      fireEvent.change(input, { target: { value: '123456' } })
      fireEvent.click(screen.getByRole('button', { name: 'Verifikasi' }))
      await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(response))
      expect(login).toHaveBeenLastCalledWith('budi', 'pw', { totp: '123456' })
    })

    it('shows an inline error for a wrong code and keeps the step', async () => {
      vi.mocked(login).mockRejectedValueOnce(totpRequired()).mockRejectedValueOnce(totpInvalid())
      const onSuccess = renderForm()
      const input = await reachSecondStep()
      fireEvent.change(input, { target: { value: '000000' } })
      fireEvent.click(screen.getByRole('button', { name: 'Verifikasi' }))
      expect(await screen.findByRole('alert')).toHaveTextContent('Kode salah')
      expect(screen.getByLabelText('Kode verifikasi')).toHaveValue('')
      expect(onSuccess).not.toHaveBeenCalled()
    })

    it('switches to a recovery code and sends it as recoveryCode', async () => {
      const response = {
        user: { username: 'budi' },
        mustChangePassword: false,
        csrfToken: 't',
        recoveryCodesRemaining: 0,
      }
      vi.mocked(login)
        .mockRejectedValueOnce(totpRequired())
        .mockResolvedValueOnce(response as never)
      const onSuccess = renderForm()
      await reachSecondStep()
      fireEvent.click(screen.getByRole('button', { name: 'Gunakan kode pemulihan' }))
      const input = await screen.findByLabelText('Kode pemulihan')
      expect(input).toHaveAttribute('inputmode', 'text')
      fireEvent.change(input, { target: { value: 'abcde-fghjk' } })
      fireEvent.click(screen.getByRole('button', { name: 'Verifikasi' }))
      await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(response))
      expect(login).toHaveBeenLastCalledWith('budi', 'pw', { recoveryCode: 'abcde-fghjk' })
    })

    it('shows the rate-limit wait on 429 and the back link returns to the credentials', async () => {
      vi.mocked(login)
        .mockRejectedValueOnce(totpRequired())
        .mockRejectedValueOnce(new ApiError('RATE_LIMITED', 429, 'slow', { retryAfter: 42 }))
      renderForm()
      const input = await reachSecondStep()
      fireEvent.change(input, { target: { value: '111111' } })
      fireEvent.click(screen.getByRole('button', { name: 'Verifikasi' }))
      expect(await screen.findByRole('alert')).toHaveTextContent('42 detik')
      expect(screen.getByRole('button', { name: 'Verifikasi' })).toBeDisabled()
      fireEvent.click(screen.getByRole('button', { name: 'Kembali' }))
      expect(await screen.findByLabelText('Username')).toHaveValue('budi')
    })
  })
})
