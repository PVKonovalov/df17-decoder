import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Root } from './Root'
import './index.css'

const root = document.getElementById('root')
if (root === null) {
  throw new Error('#root element missing')
}
createRoot(root).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
