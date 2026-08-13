import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './app/App';
import 'dockview-react/dist/styles/dockview.css';
import './styles/tokens.css';
import './styles/app.css';
import './styles/dockview.css';
import './styles/panels.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode><App /></StrictMode>
);
