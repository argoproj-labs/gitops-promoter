import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, it } from 'vitest'
import { MyTitle } from '.'

describe('MyTitle test:', () => {
  afterEach(cleanup)

  it('should render component', () => {
    render(<MyTitle title='Testing' />)
  })

  it('should render title', () => {
    render(<MyTitle title='Testing' />)
    screen.getByText('Testing')
  })
})
