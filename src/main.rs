mod cli;

fn main() {
    let mut cli = match cli::Cli::new() {
        Ok(cli) => cli,
        Err(e) => {
            polyfmt::println!("{:?}", e);
            polyfmt::finish!();
            std::process::exit(1)
        }
    };

    match cli.run() {
        Ok(_) => {}
        Err(e) => {
            // Before we start printing the error tree we need to make sure the previous fmtter is properly cleaned up.
            polyfmt::finish!();
            std::println!("{}", e);
            std::process::exit(1)
        }
    }
}
