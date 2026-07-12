import { buscarCommand } from './buscar.js';

export const commands = [buscarCommand];
export const commandsByName = new Map(commands.map((command) => [command.data.name, command]));
