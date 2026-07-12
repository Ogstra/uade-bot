import { buscarCommand } from './buscar.js';
import { detenerCommand } from './detener.js';
import { estadoCommand } from './estado.js';
import { pausarCommand } from './pausar.js';
import { reanudarCommand } from './reanudar.js';

export const commands = [buscarCommand, estadoCommand, detenerCommand, pausarCommand, reanudarCommand];
export const commandsByName = new Map(commands.map((command) => [command.data.name, command]));
