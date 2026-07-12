import { buscarCommand } from './buscar.js';
import { credencialesCommand } from './credenciales.js';
import { detenerCommand } from './detener.js';
import { estadoCommand } from './estado.js';
import { pausarCommand } from './pausar.js';
import { reanudarCommand } from './reanudar.js';
import { adminEstadoCommand } from './admin-estado.js';
import { adminDetenerCommand } from './admin-detener.js';
import { adminPausarCommand } from './admin-pausar.js';
import { adminReanudarCommand } from './admin-reanudar.js';
import { adminStatsCommand } from './admin-stats.js';

export const commands = [
  buscarCommand,
  estadoCommand,
  detenerCommand,
  pausarCommand,
  reanudarCommand,
  credencialesCommand,
  adminEstadoCommand,
  adminDetenerCommand,
  adminPausarCommand,
  adminReanudarCommand,
  adminStatsCommand,
];
export const commandsByName = new Map(commands.map((command) => [command.data.name, command]));
