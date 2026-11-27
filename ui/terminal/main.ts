import { createApp } from 'vue'
import '@xterm/xterm/css/xterm.css'
import '../src/styles/tokens.css'
import './terminal.css'
import TerminalApp from './TerminalApp.vue'

createApp(TerminalApp).mount('#app')
