import { Component, type ReactNode } from 'react'

export class QueryErrorBoundary extends Component<
  { fallback: (err: unknown, reset: () => void) => ReactNode; children: ReactNode },
  { error: unknown }
> {
  state = { error: undefined as unknown }

  static getDerivedStateFromError(error: unknown) {
    return { error }
  }

  componentDidCatch() {
    // TanStack Query's global onError handles 401 redirects; we don't
    // log here to avoid double-toasting on recoverable errors.
  }

  reset = () => this.setState({ error: undefined })

  render() {
    if (this.state.error !== undefined) {
      return this.props.fallback(this.state.error, this.reset)
    }
    return this.props.children
  }
}