var spawn = require('child_process').spawn;

const args = JSON.parse(process.env.BUILDER_ARGS_ENV)
console.log('building with entrypoint', args.entrypoint)

spawn(
    'go'
    , ['build', '-o', '/executable', args.entrypoint]
    , {detached: true, stdio: 'inherit'}
);